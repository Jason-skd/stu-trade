package app

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// === 定义部署配置 ===

// Profile 标识依赖装配和运行参数集合。
type Profile string

const (
	// ProfileDevelop 用于 Compose 本地开发环境。
	ProfileDevelop Profile = "develop"
	// ProfileNative 用于连接本机 PostgreSQL 的单文件交付物。
	ProfileNative Profile = "native"
	// ProfileProduction 用于托管生产环境。
	ProfileProduction Profile = "production"
)

// Config 包含宿主启动所需的已校验配置。
type Config struct {
	// === runtime 字段 ===
	Profile       Profile
	HTTPAddr      string
	DatabaseURL   string
	RunMigrations bool
	Version       string
	Commit        string
	LogLevel      slog.Level
}

// LoadOptions 包含优先于环境变量的进程参数。
type LoadOptions struct {
	// === runtime 字段 ===
	Profile string
	Version string
	Commit  string
}

// === 加载部署配置 ===

// LoadConfig 从显式参数、环境变量和 profile 默认值加载配置。
func LoadConfig(options LoadOptions) (Config, error) {
	// === 解析部署配置 ===
	profile, err := parseProfile(firstNonEmpty(options.Profile, os.Getenv("STUTRADE_PROFILE"), string(ProfileNative)))
	if err != nil {
		return Config{}, err
	}
	defaults := defaultsFor(profile)

	// === 校验运行参数 ===
	httpAddr := firstNonEmpty(os.Getenv("STUTRADE_HTTP_ADDR"), defaults.httpAddr)
	if err := validateHTTPAddr(httpAddr); err != nil {
		return Config{}, err
	}
	databaseURL := firstNonEmpty(os.Getenv("STUTRADE_DATABASE_URL"), defaults.databaseURL)
	if databaseURL == "" {
		return Config{}, fmt.Errorf("STUTRADE_DATABASE_URL is required for %s", profile)
	}
	if err := validateDatabaseURL(databaseURL); err != nil {
		return Config{}, err
	}
	logLevel, err := parseLogLevel(firstNonEmpty(os.Getenv("STUTRADE_LOG_LEVEL"), "info"))
	if err != nil {
		return Config{}, err
	}
	return Config{
		Profile:       profile,
		HTTPAddr:      httpAddr,
		DatabaseURL:   databaseURL,
		RunMigrations: profile != ProfileProduction,
		Version:       firstNonEmpty(options.Version, "dev"),
		Commit:        firstNonEmpty(options.Commit, "unknown"),
		LogLevel:      logLevel,
	}, nil
}

type profileDefaults struct {
	// === runtime 字段 ===
	httpAddr    string
	databaseURL string
}

// === 解析配置字段 ===

func defaultsFor(profile Profile) profileDefaults {
	if profile == ProfileNative {
		return profileDefaults{
			httpAddr:    "127.0.0.1:8080",
			databaseURL: "postgres://stutrade:stutrade@127.0.0.1:5432/stutrade?sslmode=disable",
		}
	}
	return profileDefaults{httpAddr: ":8080"}
}

func parseProfile(value string) (Profile, error) {
	profile := Profile(value)
	switch profile {
	case ProfileDevelop, ProfileNative, ProfileProduction:
		return profile, nil
	default:
		return "", fmt.Errorf("unsupported profile %q", value)
	}
}

func validateHTTPAddr(value string) error {
	_, port, err := net.SplitHostPort(value)
	if err != nil || port == "" {
		return fmt.Errorf("STUTRADE_HTTP_ADDR must be a host:port address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("STUTRADE_HTTP_ADDR must contain a valid TCP port")
	}
	return nil
}

func validateDatabaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" || parsed.Host == "" || strings.TrimPrefix(parsed.Path, "/") == "" {
		return fmt.Errorf("STUTRADE_DATABASE_URL must be a PostgreSQL URL with host and database")
	}
	return nil
}

func parseLogLevel(value string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, fmt.Errorf("STUTRADE_LOG_LEVEL must be debug, info, warn, or error: %w", err)
	}
	return level, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
