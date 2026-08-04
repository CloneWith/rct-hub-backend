package config

import (
	"os"
	"path/filepath"
	"testing"
)

func isolateConfigEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"APP_ENV", "PORT", "LOG_LEVEL",
		"MONGODB_URI", "MONGODB_NAME",
		"REDIS_ADDR", "REDIS_PASSWORD", "REDIS_DB",
		"JWT_SECRET", "JWT_EXPIRY_HOURS",
		"OSU_CLIENT_ID", "OSU_CLIENT_SECRET", "OSU_REDIRECT_URI", "OSU_API_BASE",
		"OSU_FETCHER_USER_CACHE_TTL_MIN", "OSU_FETCHER_BEATMAP_CACHE_TTL_HR",
		"ALLOWED_ORIGINS",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
}

func writeTempEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

func TestLoad_RequiresJWTSecret(t *testing.T) {
	isolateConfigEnv(t)
	_ = os.Setenv("ENV_FILE", writeTempEnv(t, "JWT_SECRET=\n"))
	t.Cleanup(func() {
		_ = os.Unsetenv("ENV_FILE")
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when JWT_SECRET is missing")
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	isolateConfigEnv(t)
	envPath := writeTempEnv(t, "JWT_SECRET=this-is-a-32-byte-secret-key-for-test!\n")
	_ = os.Setenv("ENV_FILE", envPath)
	t.Cleanup(func() {
		_ = os.Unsetenv("ENV_FILE")
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("expected default port 8080, got %s", cfg.Port)
	}
	if cfg.MongoDB.URI != "mongodb://localhost:27017/?replicaSet=rs0&directConnection=true" {
		t.Errorf("expected replica-set mongodb uri, got %s", cfg.MongoDB.URI)
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("expected default redis addr localhost:6379, got %s", cfg.Redis.Addr)
	}
	if cfg.MongoDB.Name != "rcthub" {
		t.Errorf("expected default db rcthub, got %s", cfg.MongoDB.Name)
	}
	if cfg.Redis.DB != 0 {
		t.Errorf("expected default redis db 0, got %d", cfg.Redis.DB)
	}
}

func TestLoad_FromEnvFile(t *testing.T) {
	isolateConfigEnv(t)
	envPath := writeTempEnv(t, "JWT_SECRET=this-is-a-32-byte-secret-key-for-test!\nPORT=9999\nMONGODB_URI=mongodb://custom:27017\n")

	_ = os.Setenv("ENV_FILE", envPath)
	t.Cleanup(func() {
		_ = os.Unsetenv("ENV_FILE")
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != "9999" {
		t.Errorf("expected port 9999 from env file, got %s", cfg.Port)
	}
	if cfg.MongoDB.URI != "mongodb://custom:27017" {
		t.Errorf("expected custom mongodb uri, got %s", cfg.MongoDB.URI)
	}
}

func TestLoad_MissingExplicitEnvFile(t *testing.T) {
	isolateConfigEnv(t)
	_ = os.Setenv("ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Cleanup(func() {
		_ = os.Unsetenv("ENV_FILE")
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when ENV_FILE points to missing file")
	}
}

func TestLoad_RejectsNonPositiveFetcherTTL(t *testing.T) {
	tests := []struct {
		name string
		env  string
	}{
		{name: "user", env: "OSU_FETCHER_USER_CACHE_TTL_MIN=-1\n"},
		{name: "beatmap", env: "OSU_FETCHER_BEATMAP_CACHE_TTL_HR=-1\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfigEnv(t)
			envPath := writeTempEnv(t, "JWT_SECRET=this-is-a-32-byte-secret-key-for-test!\n"+tt.env)
			t.Setenv("ENV_FILE", envPath)

			if _, err := Load(); err == nil {
				t.Fatal("expected non-positive fetcher TTL to be rejected")
			}
		})
	}
}
