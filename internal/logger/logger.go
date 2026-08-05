// Package logger provides application-wide zap logger initialization.
package logger

import (
	"rctHubBackend/internal/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds a zap.Logger based on the application environment and log level.
func New(cfg *config.Config) (*zap.Logger, error) {
	var level zap.AtomicLevel
	switch cfg.LogLevel {
	case "debug":
		level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "warn":
		level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	if cfg.AppEnv == "development" {
		zapCfg := zap.NewDevelopmentConfig()
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		zapCfg.Level = level
		return zapCfg.Build()
	}

	zapCfg := zap.NewProductionConfig()
	zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	zapCfg.Level = level
	return zapCfg.Build()
}
