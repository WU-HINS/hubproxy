package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// RegistryMapping Registry映射配置
type RegistryMapping struct {
	Upstream string `toml:"upstream"`
	AuthHost string `toml:"authHost"`
	AuthType string `toml:"authType"`
	Enabled  bool   `toml:"enabled"`
}

// DockerScope 单个上游 registry 主机的国内回源作用域。
// Host 为该 registry 的上游主机名，Base 为对应的国内回源地址。
type DockerScope struct {
	Host string `toml:"host"`
	Base string `toml:"base"`
}

// ChinaOptimizeConfig 国内访问优化配置（对应 TOML 的 [chinaOptimize] 段）。
type ChinaOptimizeConfig struct {
	// 优化模式：""（关闭/原生直连）| "backend"（后端回源改写）| "302"（重定向到代理）
	Mode string

	// 按上游 registry 主机划分的国内回源作用域。
	// 对应 TOML 的 dockerBase，兼容两种写法：
	//   - 单个字符串 "https://gh-proxy.org/docker"：仅作用于 Docker Hub（registry-1.docker.io）
	//   - 数组 [{ host = "gcr.io", base = "https://gcr.example.com" }, ...]：
	//     每项把一个 registry 主机映射到对应回源地址，可覆盖 Docker Hub / GCR / GHCR /
	//     registry.k8s.io 等任意 registry（gh-proxy 类加速源常同时支持 GCR/GHCR/K8s）。
	DockerBase []DockerScope

	// GitHub 后端回源/302 的基础地址（如 https://gh-proxy.com）。
	GitHubBase string
}

// UnmarshalTOML 自定义解析整个 [chinaOptimize] 表，使 dockerBase 同时兼容字符串与数组两种写法。
// 需要 toml.Decoder.EnableUnmarshalerInterface；data 为该表所有 key-value 的原始 TOML 文本。
// 未出现的字段保留调用前的值（即 DefaultConfig 的默认值）。
func (c *ChinaOptimizeConfig) UnmarshalTOML(data []byte) error {
	var raw struct {
		Mode       *string `toml:"mode"`
		DockerBase any     `toml:"dockerBase"`
		GitHubBase *string `toml:"githubBase"`
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Mode != nil {
		c.Mode = strings.TrimSpace(*raw.Mode)
	}
	if raw.GitHubBase != nil {
		c.GitHubBase = strings.TrimSpace(strings.TrimSuffix(*raw.GitHubBase, "/"))
	}
	if raw.DockerBase != nil {
		c.DockerBase = parseDockerScopes(raw.DockerBase)
	}
	return nil
}

// parseDockerScopes 把 dockerBase 的原始值（string 或 [{host,base},...]）解析为作用域列表。
func parseDockerScopes(v any) []DockerScope {
	switch val := v.(type) {
	case string:
		if base := strings.TrimSpace(strings.TrimSuffix(val, "/")); base != "" {
			return []DockerScope{{Host: "registry-1.docker.io", Base: base}}
		}
	case []any:
		scopes := make([]DockerScope, 0, len(val))
		for _, elem := range val {
			if m, ok := elem.(map[string]any); ok {
				if s, ok := scopeFromMap(m); ok {
					scopes = append(scopes, s)
				}
			}
		}
		return scopes
	case []map[string]any:
		scopes := make([]DockerScope, 0, len(val))
		for _, m := range val {
			if s, ok := scopeFromMap(m); ok {
				scopes = append(scopes, s)
			}
		}
		return scopes
	}
	return nil
}

func scopeFromMap(m map[string]any) (DockerScope, bool) {
	hs, _ := m["host"].(string)
	bs, _ := m["base"].(string)
	hs = strings.TrimSpace(hs)
	bs = strings.TrimSpace(strings.TrimSuffix(bs, "/"))
	if hs == "" || bs == "" {
		return DockerScope{}, false
	}
	return DockerScope{Host: hs, Base: bs}, true
}

// SetDockerHubBase 将 Docker Hub 的回源地址设为单个地址（用于默认值与环境变量覆盖）。
func (c *ChinaOptimizeConfig) SetDockerHubBase(base string) {
	base = strings.TrimSpace(strings.TrimSuffix(base, "/"))
	if base == "" {
		return
	}
	for i := range c.DockerBase {
		if c.DockerBase[i].Host == "registry-1.docker.io" {
			c.DockerBase[i].Base = base
			return
		}
	}
	c.DockerBase = append(c.DockerBase, DockerScope{Host: "registry-1.docker.io", Base: base})
}

// AppConfig 应用配置结构体
type AppConfig struct {
	Server struct {
		Host           string `toml:"host"`
		Port           int    `toml:"port"`
		FileSize       int64  `toml:"fileSize"`
		EnableH2C      bool   `toml:"enableH2C"`
		EnableFrontend bool   `toml:"enableFrontend"`
	} `toml:"server"`

	RateLimit struct {
		RequestLimit int     `toml:"requestLimit"`
		PeriodHours  float64 `toml:"periodHours"`
	} `toml:"rateLimit"`

	Security struct {
		WhiteList []string `toml:"whiteList"`
		BlackList []string `toml:"blackList"`
	} `toml:"security"`

	Access struct {
		WhiteList []string `toml:"whiteList"`
		BlackList []string `toml:"blackList"`
		Proxy     string   `toml:"proxy"`
	} `toml:"access"`

	Download struct {
		MaxImages int `toml:"maxImages"`
	} `toml:"download"`

	Registries map[string]RegistryMapping `toml:"registries"`

	TokenCache struct {
		Enabled    bool   `toml:"enabled"`
		DefaultTTL string `toml:"defaultTTL"`
	} `toml:"tokenCache"`

	// 国内访问优化配置
	ChinaOptimize ChinaOptimizeConfig `toml:"chinaOptimize"`
}

var (
	appConfig     *AppConfig
	appConfigLock sync.RWMutex

	cachedConfig     *AppConfig
	configCacheTime  time.Time
	configCacheTTL   = 5 * time.Second
	configCacheMutex sync.RWMutex
)

// DefaultConfig 返回默认配置
func DefaultConfig() *AppConfig {
	return &AppConfig{
		Server: struct {
			Host           string `toml:"host"`
			Port           int    `toml:"port"`
			FileSize       int64  `toml:"fileSize"`
			EnableH2C      bool   `toml:"enableH2C"`
			EnableFrontend bool   `toml:"enableFrontend"`
		}{
			Host:           "0.0.0.0",
			Port:           5000,
			FileSize:       2 * 1024 * 1024 * 1024,
			EnableH2C:      false,
			EnableFrontend: true,
		},
		RateLimit: struct {
			RequestLimit int     `toml:"requestLimit"`
			PeriodHours  float64 `toml:"periodHours"`
		}{
			RequestLimit: 500,
			PeriodHours:  3.0,
		},
		Security: struct {
			WhiteList []string `toml:"whiteList"`
			BlackList []string `toml:"blackList"`
		}{
			WhiteList: []string{},
			BlackList: []string{},
		},
		Access: struct {
			WhiteList []string `toml:"whiteList"`
			BlackList []string `toml:"blackList"`
			Proxy     string   `toml:"proxy"`
		}{
			WhiteList: []string{},
			BlackList: []string{},
			Proxy:     "",
		},
		Download: struct {
			MaxImages int `toml:"maxImages"`
		}{
			MaxImages: 10,
		},
		Registries: map[string]RegistryMapping{
			"ghcr.io": {
				Upstream: "ghcr.io",
				AuthHost: "ghcr.io/token",
				AuthType: "github",
				Enabled:  true,
			},
			"gcr.io": {
				Upstream: "gcr.io",
				AuthHost: "gcr.io/v2/token",
				AuthType: "google",
				Enabled:  true,
			},
			"quay.io": {
				Upstream: "quay.io",
				AuthHost: "quay.io/v2/auth",
				AuthType: "quay",
				Enabled:  true,
			},
			"registry.k8s.io": {
				Upstream: "registry.k8s.io",
				AuthHost: "registry.k8s.io",
				AuthType: "anonymous",
				Enabled:  true,
			},
		},
		TokenCache: struct {
			Enabled    bool   `toml:"enabled"`
			DefaultTTL string `toml:"defaultTTL"`
		}{
			Enabled:    true,
			DefaultTTL: "20m",
		},
		ChinaOptimize: ChinaOptimizeConfig{
			Mode:       "",
			DockerBase: []DockerScope{{Host: "registry-1.docker.io", Base: "https://gh-proxy.org/docker"}},
			GitHubBase: "https://gh-proxy.com",
		},
	}
}

// GetConfig 安全地获取配置副本
func GetConfig() *AppConfig {
	configCacheMutex.RLock()
	if cachedConfig != nil && time.Since(configCacheTime) < configCacheTTL {
		config := cachedConfig
		configCacheMutex.RUnlock()
		return config
	}
	configCacheMutex.RUnlock()

	configCacheMutex.Lock()
	defer configCacheMutex.Unlock()

	if cachedConfig != nil && time.Since(configCacheTime) < configCacheTTL {
		return cachedConfig
	}

	appConfigLock.RLock()
	if appConfig == nil {
		appConfigLock.RUnlock()
		defaultCfg := DefaultConfig()
		cachedConfig = defaultCfg
		configCacheTime = time.Now()
		return defaultCfg
	}

	configCopy := *appConfig
	configCopy.Security.WhiteList = append([]string(nil), appConfig.Security.WhiteList...)
	configCopy.Security.BlackList = append([]string(nil), appConfig.Security.BlackList...)
	configCopy.Access.WhiteList = append([]string(nil), appConfig.Access.WhiteList...)
	configCopy.Access.BlackList = append([]string(nil), appConfig.Access.BlackList...)
	appConfigLock.RUnlock()

	cachedConfig = &configCopy
	configCacheTime = time.Now()

	return cachedConfig
}

// setConfig 安全地设置配置
func setConfig(cfg *AppConfig) {
	appConfigLock.Lock()
	defer appConfigLock.Unlock()
	appConfig = cfg

	configCacheMutex.Lock()
	cachedConfig = nil
	configCacheMutex.Unlock()
}

func configFilePath() string {
	if path := strings.TrimSpace(os.Getenv("CONFIG_PATH")); path != "" {
		return path
	}
	return "config.toml"
}

func LoadConfig() error {
	cfg := DefaultConfig()
	path := configFilePath()

	if data, err := os.ReadFile(path); err == nil {
		// 启用 unstable.Unmarshaler 接口，使 [chinaOptimize] 表由 ChinaOptimizeConfig 自定义解析，
		// 从而 dockerBase 同时兼容字符串与数组两种写法。
		dec := toml.NewDecoder(bytes.NewReader(data)).EnableUnmarshalerInterface()
		if err := dec.Decode(cfg); err != nil {
			return fmt.Errorf("解析配置文件 %s 失败: %v", path, err)
		}
	} else {
		fmt.Printf("未找到配置文件 %s，使用默认配置\n", path)
	}

	overrideFromEnv(cfg)
	setConfig(cfg)

	return nil
}

// overrideFromEnv 从环境变量覆盖配置
func overrideFromEnv(cfg *AppConfig) {
	if val := os.Getenv("SERVER_HOST"); val != "" {
		cfg.Server.Host = val
	}
	if val := os.Getenv("SERVER_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil && port > 0 {
			cfg.Server.Port = port
		}
	}
	if val := os.Getenv("ENABLE_H2C"); val != "" {
		if enable, err := strconv.ParseBool(val); err == nil {
			cfg.Server.EnableH2C = enable
		}
	}
	if val := os.Getenv("ENABLE_FRONTEND"); val != "" {
		if enable, err := strconv.ParseBool(val); err == nil {
			cfg.Server.EnableFrontend = enable
		}
	}
	if val := os.Getenv("MAX_FILE_SIZE"); val != "" {
		if size, err := strconv.ParseInt(val, 10, 64); err == nil && size > 0 {
			cfg.Server.FileSize = size
		}
	}

	if val := os.Getenv("RATE_LIMIT"); val != "" {
		if limit, err := strconv.Atoi(val); err == nil && limit > 0 {
			cfg.RateLimit.RequestLimit = limit
		}
	}
	if val := os.Getenv("RATE_PERIOD_HOURS"); val != "" {
		if period, err := strconv.ParseFloat(val, 64); err == nil && period > 0 {
			cfg.RateLimit.PeriodHours = period
		}
	}

	if val := os.Getenv("IP_WHITELIST"); val != "" {
		cfg.Security.WhiteList = append(cfg.Security.WhiteList, strings.Split(val, ",")...)
	}
	if val := os.Getenv("IP_BLACKLIST"); val != "" {
		cfg.Security.BlackList = append(cfg.Security.BlackList, strings.Split(val, ",")...)
	}

	if val, ok := os.LookupEnv("ACCESS_PROXY"); ok {
		cfg.Access.Proxy = strings.TrimSpace(val)
	}

	if val := os.Getenv("MAX_IMAGES"); val != "" {
		if maxImages, err := strconv.Atoi(val); err == nil && maxImages > 0 {
			cfg.Download.MaxImages = maxImages
		}
	}

	if val, ok := os.LookupEnv("CN_OPTIMIZE_MODE"); ok {
		cfg.ChinaOptimize.Mode = strings.TrimSpace(val)
	}
	if val := os.Getenv("CN_DOCKER_BASE"); val != "" {
		cfg.ChinaOptimize.SetDockerHubBase(val)
	}
	if val := os.Getenv("CN_GITHUB_BASE"); val != "" {
		cfg.ChinaOptimize.GitHubBase = strings.TrimSpace(strings.TrimSuffix(val, "/"))
	}
}
