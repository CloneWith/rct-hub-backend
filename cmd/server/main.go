package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fatih/color"
	"go.uber.org/zap"

	"rctHubBackend/internal/config"
	"rctHubBackend/internal/database"
	"rctHubBackend/internal/logger"
	"rctHubBackend/internal/server"
)

func main() {
	color.Blue("=== RCT backend server ===")

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	log, err := logger.NewProvider(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to setup logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Close() }()
	mainLog := log.Main()
	mainLog.Info("logger set up", zap.String("log_dir", cfg.Log.Dir), zap.Strings("suppress", cfg.Log.Suppress))
	defer func() { _ = mainLog.Sync() }()

	mainLog.Info("connecting to database...")
	db, err := database.New(cfg)
	if err != nil {
		mainLog.Error("failed to connect to database", zap.Error(err))
		os.Exit(1)
	}

	mainLog.Info("checking database schema...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.EnsureIndexes(ctx); err != nil {
		mainLog.Error("failed to ensure indexes", zap.Error(err))
		os.Exit(1)
	}
	if err := db.VerifySchema(ctx); err != nil {
		mainLog.Error("database schema is not initialized or is incompatible; run cmd/initdb with migration privileges", zap.Error(err))
		os.Exit(1)
	}

	srv := server.New(cfg, db, log)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		// ErrServerClosed is an expected error.
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			mainLog.Error("server error", zap.Error(err))
		}
	}()

	mainLog.Info("server started", zap.String("port", cfg.Port))
	<-quit

	mainLog.Info("shutting down server")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Stop(shutdownCtx); err != nil {
		mainLog.Error("server shutdown error", zap.Error(err))
	}
	if err := db.Close(shutdownCtx); err != nil {
		mainLog.Error("database close error", zap.Error(err))
	}
}
