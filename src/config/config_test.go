package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigUsesConfigPathAndEnvOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.toml")
	data := []byte(`
[server]
host = "127.0.0.1"
port = 5999

[access]
proxy = "socks5://127.0.0.1:1080"
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CONFIG_PATH", path)
	t.Setenv("SERVER_PORT", "6001")
	t.Setenv("ACCESS_PROXY", "")

	if err := LoadConfig(); err != nil {
		t.Fatal(err)
	}

	cfg := GetConfig()
	if cfg.Server.Host != "127.0.0.1" {
		t.Fatalf("Server.Host = %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 6001 {
		t.Fatalf("Server.Port = %d, want 6001", cfg.Server.Port)
	}
	if cfg.Access.Proxy != "" {
		t.Fatalf("Access.Proxy = %q, want empty override", cfg.Access.Proxy)
	}
}
func TestChinaOptimizeConfigLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cn.toml")
	data := []byte(`[chinaOptimize]
		mode = "backend"
		dockerBase = "https://gh-proxy.org/docker"
		githubBase = "https://gh-proxy.com"
	`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CONFIG_PATH", path)
	if err := LoadConfig(); err != nil {
		t.Fatal(err)
	}

	cfg := GetConfig()
	if cfg.ChinaOptimize.Mode != "backend" {
		t.Fatalf("ChinaOptimize.Mode = %q, want backend", cfg.ChinaOptimize.Mode)
	}
	if cfg.ChinaOptimize.DockerBase != "https://gh-proxy.org/docker" {
		t.Fatalf("ChinaOptimize.DockerBase = %q", cfg.ChinaOptimize.DockerBase)
	}
	if cfg.ChinaOptimize.GitHubBase != "https://gh-proxy.com" {
		t.Fatalf("ChinaOptimize.GitHubBase = %q", cfg.ChinaOptimize.GitHubBase)
	}
}

func TestChinaOptimizeEnvOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cn2.toml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CONFIG_PATH", path)
	t.Setenv("CN_OPTIMIZE_MODE", "302")
	t.Setenv("CN_DOCKER_BASE", "https://gh-proxy.org/docker/")
	t.Setenv("CN_GITHUB_BASE", "https://gh-proxy.com/")

	if err := LoadConfig(); err != nil {
		t.Fatal(err)
	}

	cfg := GetConfig()
	if cfg.ChinaOptimize.Mode != "302" {
		t.Fatalf("ChinaOptimize.Mode = %q, want 302", cfg.ChinaOptimize.Mode)
	}
	if cfg.ChinaOptimize.DockerBase != "https://gh-proxy.org/docker" {
		t.Fatalf("DockerBase trailing slash not trimmed: %q", cfg.ChinaOptimize.DockerBase)
	}
	if cfg.ChinaOptimize.GitHubBase != "https://gh-proxy.com" {
		t.Fatalf("GitHubBase trailing slash not trimmed: %q", cfg.ChinaOptimize.GitHubBase)
	}
}

func TestChinaOptimizeDefaultOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cn3.toml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CONFIG_PATH", path)
	os.Unsetenv("CN_OPTIMIZE_MODE")
	if err := LoadConfig(); err != nil {
		t.Fatal(err)
	}

	cfg := GetConfig()
	if cfg.ChinaOptimize.Mode != "" {
		t.Fatalf("default mode = %q, want empty", cfg.ChinaOptimize.Mode)
	}
}
