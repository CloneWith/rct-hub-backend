package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
	if cfg.MongoDB.URI != "mongodb://localhost:27017" {
		t.Errorf("expected default mongodb uri mongodb://localhost:27017, got %s", cfg.MongoDB.URI)
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
	_ = os.Setenv("ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	t.Cleanup(func() {
		_ = os.Unsetenv("ENV_FILE")
	})

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when ENV_FILE points to missing file")
	}
}
