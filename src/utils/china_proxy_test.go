package utils

import (
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
	got := rewriteDockerUpstreamURL(u, base)
	if got == nil {
		t.Fatal("expected rewrite, got nil")
	}
	want := "https://docker.1ms.run/v2/library/nginx/manifests/latest"
	if got.String() != want {
		t.Fatalf("got %q want %q", got.String(), want)
	}
}

func TestRewriteDockerUpstreamURL_NonDockerHub(t *testing.T) {
	const base = "https://docker.1ms.run"
	// ghcr 等非 Docker Hub 的 registry 不走 dockerBase，保持原路由
	u := mustURL(t, "https://ghcr.io/v2/foo/bar/manifests/latest")
	if got := rewriteDockerUpstreamURL(u, base); got != nil {
		t.Fatalf("non-Docker-Hub should not rewrite, got %q", got.String())
	}
}

func TestRewriteDockerUpstreamURL_EmptyBase(t *testing.T) {
	u := mustURL(t, "https://registry-1.docker.io/v2/library/nginx/manifests/latest")
	if got := rewriteDockerUpstreamURL(u, ""); got != nil {
		t.Fatalf("expected nil when base empty, got %q", got.String())
	}
}

func TestRewriteDockerUpstreamURL_KeepsQuery(t *testing.T) {
	const base = "https://docker.1ms.run"
	u := mustURL(t, "https://registry-1.docker.io/v2/library/nginx/tags/list?n=10&last=abc")
	got := rewriteDockerUpstreamURL(u, base)
	if got == nil {
		t.Fatal("expected rewrite")
	}
	if got.RawQuery != "n=10&last=abc" {
		t.Fatalf("query lost: %q", got.RawQuery)
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
