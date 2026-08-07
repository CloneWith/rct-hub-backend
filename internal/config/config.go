package config

import (
	"fmt"
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
	Log         LogConfig
	MongoDB     MongoDBConfig
	Redis       RedisConfig
	JWT         JWTConfig
	Osu         OsuConfig
	CORS        CORSConfig
}

// LogConfig holds logging configuration.
type LogConfig struct {
	// Dir is the directory for log files. If empty, logs go to stdout only.
	Dir string
	// Suppress is a blacklist of log categories that will NOT be recorded.
	// All predefined categories (see logger.AllCategories) get their own log
	// files by default; list categories here to silence them entirely.
	// The "runtime" category (main logger) is never suppressed.
	Suppress []string
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

// Load reads configuration from environment variables.
// It first attempts to load a .env file so that local development values
// can be committed to the repo via .env.example.
func Load() (*Config, error) {
	if err := loadDotEnv(); err != nil {
		return nil, err
	}

	cfg := &Config{
		AppEnv:      getEnv("APP_ENV", "development"),
		Port:        getEnv("PORT", "8080"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
		FrontEndURI: getEnv("FRONTEND_URI", "http://localhost:3000"),
		Log: LogConfig{
			Dir:      getEnv("LOG_DIR", "./logs"),
			Suppress: parseCSV(getEnv("LOG_SUPPRESS", "")),
		},
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
			AllowedOrigins: strings.Split(getEnv("ALLOWED_ORIGINS", "*"), ","),
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
	if c.Osu.FetcherUserCacheTTL <= 0 {
		return fmt.Errorf("OSU_FETCHER_USER_CACHE_TTL_MIN must be greater than zero")
	}
	if c.Osu.FetcherBeatmapCacheTTL <= 0 {
		return fmt.Errorf("OSU_FETCHER_BEATMAP_CACHE_TTL_HR must be greater than zero")
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

// parseCSV splits a comma-separated string into trimmed, non-empty parts.
func parseCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
