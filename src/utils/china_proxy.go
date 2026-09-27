package utils

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hubproxy/config"
)

// dockerScopeBase 根据上游 registry 主机名（如 registry-1.docker.io / gcr.io / ghcr.io /
// registry.k8s.io）在 ChinaOptimize.DockerBase 配置的作用域列表中查找对应国内回源地址；
// 未配置该主机则返回空串（保持原路由直连）。
func dockerScopeBase(host string) string {
	cfg := config.GetConfig()
	if cfg.ChinaOptimize.Mode != "backend" {
		return ""
	}
	for _, s := range cfg.ChinaOptimize.DockerBase {
		if s.Host == host && s.Base != "" {
			return s.Base
		}
	}
	return ""
}

// rewriteDockerUpstreamURL 将指定主机（host）的上游 registry URL 改写为国内回源 (base) 地址。
// 仅当上游主机与 host 一致且 base 非空时改写：
//
//	https://{host}/v2/library/nginx/... → {base}/v2/library/nginx/...
//
// 主机不匹配或 base 为空一律返回 nil（不改写，保持原路由）。
// base 例如 "https://docker.1ms.run" 或 "https://gcr.example.com"。
func rewriteDockerUpstreamURL(u *url.URL, host, base string) *url.URL {
	if base == "" || host == "" {
		return nil
	}
	baseURL, err := url.Parse(base)
	if err != nil || baseURL.Host == "" {
		return nil
	}

	if u.Hostname() != host {
		// 主机不匹配：不走该回源（未配置的 registry 保持直连）
		return nil
	}

	// 保留原 path（通常以 /v2/... 开头）
	path := u.Path
	if path == "" {
		path = "/"
	}

	nu := *u
	nu.Scheme = baseURL.Scheme
	nu.Host = baseURL.Host
	nu.Path = strings.TrimSuffix(baseURL.Path, "/") + path
	// query 保留
	return &nu
}

// chinaBackendTransport 国内后端回源改写 transport。
// 包装底层 RoundTripper，在 https 请求发出前改写 docker registry 上游地址到反代。
type chinaBackendTransport struct {
	base http.RoundTripper
}

// RoundTrip 实现 http.RoundTripper。
func (t *chinaBackendTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg := config.GetConfig()
	if cfg.ChinaOptimize.Mode != "backend" {
		return t.base.RoundTrip(req)
	}

	// 有鉴权的请求（携带 Authorization 或 Basic 凭据）直接走原路由，
	// 由 go-containerregistry 正常直连上游真实 registry 完成鉴权；
	// 国内回源（如公共镜像加速源）通常无法代理私有/需登录镜像。
	if hasAuthRequest(req) {
		return t.base.RoundTrip(req)
	}

	// 按上游 registry 主机名在 dockerBase 作用域列表中查找对应回源地址，
	// 支持 Docker Hub / gcr / ghcr / registry.k8s.io 等多个 registry 分别配置。
	host := req.URL.Hostname()
	base := dockerScopeBase(host)
	if base == "" {
		return t.base.RoundTrip(req)
	}

	u := rewriteDockerUpstreamURL(req.URL, host, base)
	if u == nil {
		return t.base.RoundTrip(req)
	}

	clone := req.Clone(req.Context())
	clone.URL = u
	clone.Host = u.Host
	return t.base.RoundTrip(clone)
}

// hasAuthRequest 判断请求是否携带鉴权凭据。
// 命中即意味着该镜像需要登录/鉴权，应跳过国内回源改写。
func hasAuthRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	if req.Header.Get("Authorization") != "" {
		return true
	}
	// Basic 认证也可能以 userinfo 形式内嵌在 URL 中
	if req.URL != nil && req.URL.User != nil {
		return true
	}
	// 某些客户端通过 Proxy-Authorization 走凭据
	if req.Header.Get("Proxy-Authorization") != "" {
		return true
	}
	return false
}

// globalChinaTransport 全局共享的后端回源改写 transport（延迟创建）。
var globalChinaTransport http.RoundTripper

// GetDockerTransport 返回用于 docker registry 远端调用的 transport。
// backend 模式下会将上游请求改写为国内反代地址；否则原样透传。
// 注意：go-containerregistry 的 digest 自校验不依赖 URL host，因此改写不影响校验。
func GetDockerTransport() http.RoundTripper {
	if globalChinaTransport == nil {
		globalChinaTransport = &chinaBackendTransport{base: GetGlobalHTTPClient().Transport}
	}
	return globalChinaTransport
}

// GitHubBackendURL 计算国内反代下 GitHub 原始 URL 的改写地址。
// 仅 backend 模式返回改写地址；否则返回原 URL。
func GitHubBackendURL(rawURL string) string {
	cfg := config.GetConfig()
	if cfg.ChinaOptimize.Mode != "backend" {
		return rawURL
	}
	base := strings.TrimSuffix(cfg.ChinaOptimize.GitHubBase, "/")
	if base == "" {
		return rawURL
	}
	return base + "/" + rawURL
}

// GitHubRedirectURL 计算 302 模式下的重定向地址；非 302 模式返回空串。
func GitHubRedirectURL(rawURL string) string {
	cfg := config.GetConfig()
	if cfg.ChinaOptimize.Mode != "302" {
		return ""
	}
	base := strings.TrimSuffix(cfg.ChinaOptimize.GitHubBase, "/")
	if base == "" {
		return ""
	}
	return base + "/" + rawURL
}

// ChinaBackendMode 返回当前是否启用国内回源 backend（代理）模式。
func ChinaBackendMode() bool {
	return config.GetConfig().ChinaOptimize.Mode == "backend"
}

// ResolveRedirectTarget 提前请求 rawURL 并跟随 3xx 重定向，返回链尾的最终地址。
// 用于把国内反代（gh-proxy 等）自身产生的 302 提前"扁平化"：302 模式下直接把
// 客户端指向最终地址，避免客户端经历多次 302 跳转。
// 未发生跳转或解析失败时返回空串。结果按 rawURL 做短 TTL 缓存以降低重复开销。
func ResolveRedirectTarget(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	key := BuildCacheKey("redirect", rawURL)
	if cached := GlobalCache.GetToken(key); cached != "" {
		return cached
	}
	final, err := resolveRedirectTarget(rawURL)
	if err != nil || final == "" || final == rawURL {
		return ""
	}
	GlobalCache.SetToken(key, final, 60*time.Second)
	return final
}

// resolveRedirectTarget 发起一次 GET(Range: bytes=0-0) 请求并跟随重定向，
// 返回链尾 URL（通过 resp.Request.URL 获取），避免真正下载整个文件。
func resolveRedirectTarget(rawURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	// 只取首字节，避免把整个文件下载下来
	req.Header.Set("Range", "bytes=0-0")
	client := GetGlobalHTTPClient()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
	if resp.Request == nil || resp.Request.URL == nil {
		return "", nil
	}
	return resp.Request.URL.String(), nil
}
