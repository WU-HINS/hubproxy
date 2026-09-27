package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"hubproxy/config"
	"hubproxy/utils"
)

func TestCheckGitHubURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		user string
		repo string
	}{
		{"release", "https://github.com/user/repo/releases/download/v1/file.tar.gz", "user", "repo"},
		{"raw", "https://raw.githubusercontent.com/user/repo/main/file.sh", "user", "repo"},
		{"api", "https://api.github.com/repos/user/repo/releases/latest", "user", "repo"},
		{"huggingface", "https://huggingface.co/user/model/resolve/main/file", "user", "model/resolve/main/file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckGitHubURL(tt.url)
			if len(got) < 2 || got[0] != tt.user || got[1] != tt.repo {
				t.Fatalf("CheckGitHubURL(%q) = %#v", tt.url, got)
			}
		})
	}
}

func TestCheckGitHubURLRejectsOtherHosts(t *testing.T) {
	if got := CheckGitHubURL("https://example.com/user/repo/file"); got != nil {
		t.Fatalf("unexpected match: %#v", got)
	}
	if got := CheckGitHubURL("https://download.docker.com/linux/static/stable/x86_64/docker.tgz"); got != nil {
		t.Fatalf("download.docker.com should be rejected: %#v", got)
	}
}

// loadChinaConfigForTest 写入并加载一段临时 config.toml，并初始化 HTTP 客户端。
func loadChinaConfigForTest(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_PATH", path)
	if err := config.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	utils.InitHTTPClients()
}

// TestBackendModeFlattensRedirectThroughProxy 验证 backend（代理）模式下，
// 反代自身返回的 302 会在服务端逐跳扁平化，且跳转到的 GitHub 地址会被重新回源改写
// （而不是直连 GitHub）；客户端最终只拿到内容响应，看不到 302。
func TestBackendModeFlattensRedirectThroughProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var hits int32
	ghProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		switch {
		case strings.Contains(r.URL.Path, "/real.bin"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("REAL"))
		case strings.Contains(r.URL.Path, "/a.bin"):
			// 模拟国内反代把请求 302 回 GitHub 家族地址
			http.Redirect(w, r, "https://github.com/u/r/releases/download/v1/real.bin", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ghProxy.Close()

	loadChinaConfigForTest(t, "[chinaOptimize]\n\tmode = \"backend\"\n\tgithubBase = \""+ghProxy.URL+"\"\n")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/github.com/u/r/releases/download/v1/a.bin", nil)
	GitHubProxyHandler(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	if w.Body.String() != "REAL" {
		t.Fatalf("body = %q, want REAL", w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("client should not receive a redirect, got Location=%q", loc)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected 2 server-side hops through the proxy, got %d", got)
	}
}

// Test302ModeRedirectsStraightToFinal 验证 302 模式下会把反代自身的 302 提前解析，
// 客户端只收到一跳、且直接指向最终地址。
func Test302ModeRedirectsStraightToFinal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("asset"))
	}))
	defer final.Close()

	ghProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/final.bin", http.StatusFound)
	}))
	defer ghProxy.Close()

	loadChinaConfigForTest(t, "[chinaOptimize]\n\tmode = \"302\"\n\tgithubBase = \""+ghProxy.URL+"\"\n")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/github.com/u/r/releases/download/v1/a.bin", nil)
	GitHubProxyHandler(c)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != final.URL+"/final.bin" {
		t.Fatalf("Location = %q, want %q", loc, final.URL+"/final.bin")
	}
}
