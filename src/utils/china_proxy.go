package utils

import (
	"net/http"
	"net/url"
	"strings"

	"hubproxy/config"
)

// chinaDockerHubHost hubproxy 里默认的 docker hub 上游主机（与 handlers/docker.go 的 name.NewRegistry 一致）。
// registry-1.docker.io 指向 Docker Hub。
const chinaDockerHubHost = "registry-1.docker.io"

// rewriteDockerUpstreamURL 将 Docker Hub 上游 URL 改写为国内 dockerBase 加速源地址。
// 仅当上游是 Docker Hub（registry-1.docker.io）且 base 非空时改写：
//   https://registry-1.docker.io/v2/library/nginx/... → {base}/v2/library/nginx/...
// 其它 registry（ghcr/gcr/quay/k8s 等）一律返回 nil（不改写，保持原路由）。
// base 例如 "https://docker.1ms.run"。
func rewriteDockerUpstreamURL(u *url.URL, base string) *url.URL {
	if base == "" {
		return nil
	}
	baseURL, err := url.Parse(base)
	if err != nil || baseURL.Host == "" {
		return nil
	}

	if u.Hostname() != chinaDockerHubHost {
		// 非 Docker Hub：不走 dockerBase（加速源通常无法代理 ghcr/quay 等）
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
	if cfg.ChinaOptimize.Mode != "backend" || cfg.ChinaOptimize.DockerBase == "" {
		return t.base.RoundTrip(req)
	}

	// 有鉴权的请求（携带 Authorization 或 Basic 凭据）直接走原路由，
	// 由 go-containerregistry 正常直连上游真实 registry 完成鉴权；
	// dockerBase（如公共镜像加速源）通常无法代理私有/需登录镜像。
	if hasAuthRequest(req) {
		return t.base.RoundTrip(req)
	}

	u := rewriteDockerUpstreamURL(req.URL, cfg.ChinaOptimize.DockerBase)
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
