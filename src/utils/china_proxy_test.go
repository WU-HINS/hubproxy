package utils

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"hubproxy/config"
)

// loadCabTestConfig 加载一段临时 config.toml。
func loadCabTestConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_PATH", path)
	if err := config.LoadConfig(); err != nil {
		t.Fatal(err)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestRewriteDockerUpstreamURL_DockerHub(t *testing.T) {
	const base = "https://docker.1ms.run"
	u := mustURL(t, "https://registry-1.docker.io/v2/library/nginx/manifests/latest")
	got := rewriteDockerUpstreamURL(u, "registry-1.docker.io", base)
	if got == nil {
		t.Fatal("expected rewrite, got nil")
	}
	want := "https://docker.1ms.run/v2/library/nginx/manifests/latest"
	if got.String() != want {
		t.Fatalf("got %q want %q", got.String(), want)
	}
}

func TestRewriteDockerUpstreamURL_HostMismatch(t *testing.T) {
	const base = "https://docker.1ms.run"
	// 指定 Docker Hub 主机，但上游是 ghcr.io：主机不匹配不改写
	u := mustURL(t, "https://ghcr.io/v2/foo/bar/manifests/latest")
	if got := rewriteDockerUpstreamURL(u, "registry-1.docker.io", base); got != nil {
		t.Fatalf("host mismatch should not rewrite, got %q", got.String())
	}
}

func TestRewriteDockerUpstreamURL_GCR(t *testing.T) {
	const base = "https://gcr.example.com"
	u := mustURL(t, "https://gcr.io/v2/foo/bar/manifests/latest")
	got := rewriteDockerUpstreamURL(u, "gcr.io", base)
	if got == nil {
		t.Fatal("expected rewrite for gcr.io")
	}
	want := "https://gcr.example.com/v2/foo/bar/manifests/latest"
	if got.String() != want {
		t.Fatalf("got %q want %q", got.String(), want)
	}
}

func TestRewriteDockerUpstreamURL_EmptyBase(t *testing.T) {
	u := mustURL(t, "https://registry-1.docker.io/v2/library/nginx/manifests/latest")
	if got := rewriteDockerUpstreamURL(u, "registry-1.docker.io", ""); got != nil {
		t.Fatalf("expected nil when base empty, got %q", got.String())
	}
}

func TestRewriteDockerUpstreamURL_KeepsQuery(t *testing.T) {
	const base = "https://docker.1ms.run"
	u := mustURL(t, "https://registry-1.docker.io/v2/library/nginx/tags/list?n=10&last=abc")
	got := rewriteDockerUpstreamURL(u, "registry-1.docker.io", base)
	if got == nil {
		t.Fatal("expected rewrite")
	}
	if got.RawQuery != "n=10&last=abc" {
		t.Fatalf("query lost: %q", got.RawQuery)
	}
}

// TestDockerScopeBase_ArrayConfig 验证数组形式 dockerBase 按寄存器主机划分作用域。
func TestDockerScopeBase_ArrayConfig(t *testing.T) {
	loadCabTestConfig(t, `[chinaOptimize]
		mode = "backend"
		dockerBase = [
			{ host = "registry-1.docker.io", base = "https://gh-proxy.org/docker" },
			{ host = "gcr.io", base = "https://gcr.example.com" },
			{ host = "ghcr.io", base = "https://ghcr.example.com" },
			{ host = "registry.k8s.io", base = "https://k8s.example.com" },
		]
	`)

	cases := []struct {
		host, want string
	}{
		{"registry-1.docker.io", "https://gh-proxy.org/docker"},
		{"gcr.io", "https://gcr.example.com"},
		{"ghcr.io", "https://ghcr.example.com"},
		{"registry.k8s.io", "https://k8s.example.com"},
		{"quay.io", ""},
	}
	for _, c := range cases {
		if got := dockerScopeBase(c.host); got != c.want {
			t.Fatalf("dockerScopeBase(%q) = %q, want %q", c.host, got, c.want)
		}
	}
}

// TestDockerScopeBase_StringConfig 验证字符串形式 dockerBase 仅作用于 Docker Hub。
func TestDockerScopeBase_StringConfig(t *testing.T) {
	loadCabTestConfig(t, `[chinaOptimize]
		mode = "backend"
		dockerBase = "https://gh-proxy.org/docker"
	`)

	if got := dockerScopeBase("registry-1.docker.io"); got != "https://gh-proxy.org/docker" {
		t.Fatalf("dockerScopeBase(docker hub) = %q, want %q", got, "https://gh-proxy.org/docker")
	}
	if got := dockerScopeBase("gcr.io"); got != "" {
		t.Fatalf("dockerScopeBase(gcr.io) should be empty with string base, got %q", got)
	}
}

// TestDockerScopeBase_Disabled 验证非 backend 模式不查找作用域（保持空）。
func TestDockerScopeBase_Disabled(t *testing.T) {
	loadCabTestConfig(t, `[chinaOptimize]
		mode = ""
	`)
	if got := dockerScopeBase("registry-1.docker.io"); got != "" {
		t.Fatalf("disabled mode should give empty scope, got %q", got)
	}
}

func TestGitHubBackendURL(t *testing.T) {
	loadCabTestConfig(t, `[chinaOptimize]
		mode = "backend"
		githubBase = "https://gh-proxy.com"
	`)
	got := GitHubBackendURL("https://github.com/owner/repo/releases/download/v1/a.tar.gz")
	want := "https://gh-proxy.com/https://github.com/owner/repo/releases/download/v1/a.tar.gz"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGitHubBackendURL_Disabled(t *testing.T) {
	loadCabTestConfig(t, `[chinaOptimize]
		mode = ""
	`)
	raw := "https://github.com/owner/repo/releases/download/v1/a.tar.gz"
	if got := GitHubBackendURL(raw); got != raw {
		t.Fatalf("disabled mode should not rewrite, got %q", got)
	}
}

func TestGitHubRedirectURL(t *testing.T) {
	loadCabTestConfig(t, `[chinaOptimize]
		mode = "302"
		githubBase = "https://gh-proxy.com"
	`)
	got := GitHubRedirectURL("https://github.com/owner/repo/archive/refs/heads/main.zip")
	want := "https://gh-proxy.com/https://github.com/owner/repo/archive/refs/heads/main.zip"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGitHubRedirectURL_Not302(t *testing.T) {
	loadCabTestConfig(t, `[chinaOptimize]
		mode = "backend"
	`)
	if got := GitHubRedirectURL("https://github.com/owner/repo/file"); got != "" {
		t.Fatalf("non-302 mode should return empty, got %q", got)
	}
}

func TestHasAuthRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "https://registry-1.docker.io/v2/library/nginx/manifests/latest", nil)
	if hasAuthRequest(req) {
		t.Fatal("plain request should not have auth")
	}

	reqAuth := httptest.NewRequest("GET", "https://registry-1.docker.io/v2/library/nginx/manifests/latest", nil)
	reqAuth.Header.Set("Authorization", "Bearer xxx")
	if !hasAuthRequest(reqAuth) {
		t.Fatal("Bearer auth should be detected")
	}

	reqBasic := httptest.NewRequest("GET", "https://user:pass@registry-1.docker.io/v2/library/nginx/manifests/latest", nil)
	if !hasAuthRequest(reqBasic) {
		t.Fatal("URL userinfo auth should be detected")
	}
}

// TestResolveRedirectTarget_FollowsChain 验证能提前跟随多跳 302 并返回最终地址。
func TestResolveRedirectTarget_FollowsChain(t *testing.T) {
	InitHTTPClients()

	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("final"))
	}))
	defer final.Close()

	mid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/asset", http.StatusFound)
	}))
	defer mid.Close()

	got := ResolveRedirectTarget(mid.URL + "/start")
	want := final.URL + "/asset"
	if got != want {
		t.Fatalf("ResolveRedirectTarget = %q, want %q", got, want)
	}
}

// TestResolveRedirectTarget_NoRedirect 验证未发生跳转时返回空串（调用方回退原地址）。
func TestResolveRedirectTarget_NoRedirect(t *testing.T) {
	InitHTTPClients()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("direct"))
	}))
	defer srv.Close()

	if got := ResolveRedirectTarget(srv.URL + "/file"); got != "" {
		t.Fatalf("no-redirect target should be empty, got %q", got)
	}
	if got := ResolveRedirectTarget(""); got != "" {
		t.Fatalf("empty input should yield empty, got %q", got)
	}
}
