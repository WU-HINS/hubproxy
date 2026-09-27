package utils

import (
	"net"
	"net/http"
	"os"
	"time"

	"hubproxy/config"
)

var (
	globalHTTPClient *http.Client
	searchHTTPClient *http.Client
	githubHTTPClient *http.Client
)

// InitHTTPClients 初始化HTTP客户端
func InitHTTPClients() {
	cfg := config.GetConfig()

	if p := cfg.Access.Proxy; p != "" {
		os.Setenv("HTTP_PROXY", p)
		os.Setenv("HTTPS_PROXY", p)
	}

	globalHTTPClient = &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          1000,
			MaxIdleConnsPerHost:   1000,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 300 * time.Second,
		},
	}

	searchHTTPClient = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
			DisableCompression:  false,
		},
	}
}

// GetGlobalHTTPClient 获取全局HTTP客户端
func GetGlobalHTTPClient() *http.Client {
	return globalHTTPClient
}

// GetGitHubHTTPClient 返回 GitHub 代理专用客户端：不自动跟随 3xx 重定向。
// 由上层逐跳解析，这样在 backend（代理）模式下可以对每一跳的 GitHub 地址
// 重新做国内回源改写，并把 302 在服务端一次性扁平化，避免客户端经历多次 302。
func GetGitHubHTTPClient() *http.Client {
	if githubHTTPClient == nil {
		tr := http.DefaultTransport
		if base := GetGlobalHTTPClient(); base != nil && base.Transport != nil {
			tr = base.Transport
		}
		githubHTTPClient = &http.Client{
			Transport: tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return githubHTTPClient
}

// GetSearchHTTPClient 获取搜索HTTP客户端
func GetSearchHTTPClient() *http.Client {
	return searchHTTPClient
}
