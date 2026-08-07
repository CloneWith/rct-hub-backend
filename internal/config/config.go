package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration for the application.
type Config struct {
	AppEnv      string
	Port        string
	LogLevel    string
	FrontEndURI string
	MongoDB     MongoDBConfig
	Redis       RedisConfig
	JWT         JWTConfig
	Osu         OsuConfig
	CORS        CORSConfig
	AuthCookie  AuthCookieConfig
	AuthSession AuthSessionConfig
}

type MongoDBConfig struct {
	URI  string
	Name string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type JWTConfig struct {
	Secret string
	Expiry time.Duration
}

type OsuConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	APIBase      string

	// Fetcher cache TTLs (optional, defaults applied in the fetcher package).
	FetcherUserCacheTTL    time.Duration
	FetcherBeatmapCacheTTL time.Duration
}

type CORSConfig struct {
	AllowedOrigins []string
}

type AuthCookieConfig struct {
	Name     string
	Domain   string
	Secure   bool
	SameSite string
}

type AuthSessionConfig struct {
	IdleExpiry     time.Duration
	AbsoluteExpiry time.Duration
}

// Load reads configuration from environment variables.
// It first attempts to load a .env file so that local development values
// can be committed to the repo via .env.example.
func Load() (*Config, error) {
	if err := loadDotEnv(); err != nil {
		return nil, err
	}

	appEnv := getEnv("APP_ENV", "development")
	frontEndURI := getEnv("FRONTEND_URI", "http://localhost:3000")
	cookieSecure, err := getEnvBool("AUTH_COOKIE_SECURE", appEnv == "production")
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		AppEnv:      appEnv,
		Port:        getEnv("PORT", "8080"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		FrontEndURI: frontEndURI,
		MongoDB: MongoDBConfig{
			URI:  getEnv("MONGODB_URI", "mongodb://localhost:27017/?replicaSet=rs0&directConnection=true"),
			Name: getEnv("MONGODB_NAME", "rcthub"),
		},
		Redis: RedisConfig{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       mustAtoi(getEnv("REDIS_DB", "0")),
		},
		JWT: JWTConfig{
			Secret: getEnv("JWT_SECRET", ""),
			Expiry: time.Duration(mustAtoi(getEnv("JWT_EXPIRY_HOURS", "168"))) * time.Hour,
		},
		Osu: OsuConfig{
			ClientID:               getEnv("OSU_CLIENT_ID", ""),
			ClientSecret:           getEnv("OSU_CLIENT_SECRET", ""),
			RedirectURI:            getEnv("OSU_REDIRECT_URI", "http://localhost:8080/auth/osu/callback"),
			APIBase:                getEnv("OSU_API_BASE", "https://osu.ppy.sh"),
			FetcherUserCacheTTL:    time.Duration(mustAtoi(getEnv("OSU_FETCHER_USER_CACHE_TTL_MIN", "30"))) * time.Minute,
			FetcherBeatmapCacheTTL: time.Duration(mustAtoi(getEnv("OSU_FETCHER_BEATMAP_CACHE_TTL_HR", "24"))) * time.Hour,
		},
		CORS: CORSConfig{
			AllowedOrigins: splitTrimmed(getEnv("ALLOWED_ORIGINS", frontEndURI)),
		},
		AuthCookie: AuthCookieConfig{
			Name: getEnv("AUTH_COOKIE_NAME", "rcthub_session"), Domain: getEnv("AUTH_COOKIE_DOMAIN", ""),
			Secure:   cookieSecure,
			SameSite: strings.ToLower(getEnv("AUTH_COOKIE_SAME_SITE", "lax")),
		},
		AuthSession: AuthSessionConfig{
			IdleExpiry:     time.Duration(mustAtoi(getEnv("AUTH_SESSION_IDLE_HOURS", "24"))) * time.Hour,
			AbsoluteExpiry: time.Duration(mustAtoi(getEnv("AUTH_SESSION_MAX_HOURS", "168"))) * time.Hour,
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadDotEnv loads the .env file if one is specified or found in common locations.
// It uses Overload so that values in .env take precedence over existing environment
// variables, matching the expectation that local development settings override defaults.
func loadDotEnv() error {
	path := os.Getenv("ENV_FILE")
	if path != "" {
		if err := godotenv.Overload(path); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("ENV_FILE %q does not exist", path)
			}
			return fmt.Errorf("failed to load ENV_FILE %q: %w", path, err)
		}
		return nil
	}

	candidates := dotEnvCandidates()
	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := godotenv.Overload(path); err != nil {
			return fmt.Errorf("failed to load %q: %w", path, err)
		}
		return nil
	}

	return nil
}

// dotEnvCandidates returns likely locations for the .env file.
func dotEnvCandidates() []string {
	candidates := []string{".env"}

	// When running tests or a binary from outside the project root, fall back to
	// the directory containing this source file (project root /internal/config).
	_, file, _, ok := runtime.Caller(0)
	if ok {
		projectRoot := filepath.Join(filepath.Dir(file), "..", "..")
		projectRoot, _ = filepath.Abs(projectRoot)
		candidates = append(candidates, filepath.Join(projectRoot, ".env"))
	}

	// Also try the executable's directory for compiled binaries.
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}

	return candidates
}

func (c *Config) validate() error {
	if c.JWT.Secret == "" {
		return fmt.Errorf("JWT_SECRET is required")
	}
	if len(c.JWT.Secret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 bytes")
	}
	if c.JWT.Expiry <= 0 {
		return fmt.Errorf("JWT_EXPIRY_HOURS must be greater than zero")
	}
	if c.Osu.FetcherUserCacheTTL <= 0 {
		return fmt.Errorf("OSU_FETCHER_USER_CACHE_TTL_MIN must be greater than zero")
	}
	if c.Osu.FetcherBeatmapCacheTTL <= 0 {
		return fmt.Errorf("OSU_FETCHER_BEATMAP_CACHE_TTL_HR must be greater than zero")
	}
	if c.AuthCookie.Name == "" || strings.ContainsAny(c.AuthCookie.Name, "=; ,\t\r\n") {
		return fmt.Errorf("AUTH_COOKIE_NAME is invalid")
	}
	if c.AuthCookie.SameSite != "lax" && c.AuthCookie.SameSite != "strict" {
		return fmt.Errorf("AUTH_COOKIE_SAME_SITE must be lax or strict")
	}
	if c.AuthSession.IdleExpiry <= 0 {
		return fmt.Errorf("AUTH_SESSION_IDLE_HOURS must be greater than zero")
	}
	if c.AuthSession.AbsoluteExpiry <= 0 || c.AuthSession.AbsoluteExpiry < c.AuthSession.IdleExpiry {
		return fmt.Errorf("AUTH_SESSION_MAX_HOURS must be at least AUTH_SESSION_IDLE_HOURS")
	}
	if c.AppEnv == "production" && !c.AuthCookie.Secure {
		return fmt.Errorf("AUTH_COOKIE_SECURE must be true in production")
	}
	frontEnd, err := url.Parse(c.FrontEndURI)
	if err != nil || frontEnd.Scheme == "" || frontEnd.Host == "" || frontEnd.Path != "" || frontEnd.RawQuery != "" || frontEnd.Fragment != "" {
		return fmt.Errorf("FRONTEND_URI must be an exact origin")
	}
	if c.AppEnv == "production" && frontEnd.Scheme != "https" {
		return fmt.Errorf("FRONTEND_URI must use https in production")
	}
	if len(c.CORS.AllowedOrigins) == 0 {
		return fmt.Errorf("ALLOWED_ORIGINS must contain at least one exact origin")
	}
	for _, origin := range c.CORS.AllowedOrigins {
		if origin == "*" {
			return fmt.Errorf("ALLOWED_ORIGINS cannot contain wildcard when browser credentials are enabled")
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("ALLOWED_ORIGINS contains invalid exact origin %q", origin)
		}
		if c.AppEnv == "production" && parsed.Scheme != "https" {
			return fmt.Errorf("ALLOWED_ORIGINS must use https in production")
		}
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func getEnvBool(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return parsed, nil
}

func splitTrimmed(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}
