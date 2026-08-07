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

	"go.uber.org/zap"

	"rctHubBackend/internal/config"
	"rctHubBackend/internal/database"
	"rctHubBackend/internal/logger"
	"rctHubBackend/internal/server"
)

func main() {
	fmt.Println("=== RCT backend server ===")

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	log, err := logger.New(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to setup logger: %v\n", err)
		os.Exit(1)
	}
	log.Info("logger set up")
	defer func() { _ = log.Sync() }()

	log.Info("connecting to database...")
	db, err := database.New(cfg)
	if err != nil {
		log.Error("failed to connect to database", zap.Error(err))
		os.Exit(1)
	}

	log.Info("checking database schema...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.EnsureIndexes(ctx); err != nil {
		log.Error("failed to ensure indexes", zap.Error(err))
		os.Exit(1)
	}
	if err := db.VerifySchema(ctx); err != nil {
		log.Error("database schema is not initialized or is incompatible; run cmd/initdb with migration privileges", zap.Error(err))
		os.Exit(1)
	}

	srv := server.New(cfg, db, log)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		// ErrServerClosed is an expected error.
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", zap.Error(err))
		}
	}()

	log.Info("server started", zap.String("port", cfg.Port))
	<-quit

	log.Info("shutting down server")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Stop(shutdownCtx); err != nil {
		log.Error("server shutdown error", zap.Error(err))
	}
	if err := db.Close(shutdownCtx); err != nil {
		log.Error("database close error", zap.Error(err))
	}
}
