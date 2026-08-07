// Package logger provides application-wide zap logger initialization with
// file output and per-category log file support.
//
// Log files are named with the server startup timestamp (e.g. runtime-1234567890.log)
// and written to the directory configured via LOG_DIR (default: ./logs).
//
// The "runtime" category IS the main logger (Provider.Main / logger.New).
// All other predefined categories (see AllCategories) get their own log files.
// Categories listed in the LOG_SUPPRESS environment variable are silenced
// (their logs are not recorded). The runtime logger is always active.
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"rctHubBackend/internal/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ---------------------------------------------------------------------------
// Global file tracking
// ---------------------------------------------------------------------------

// startTime is set once on the first logger creation. All log files created
// during this process lifetime share this timestamp in their filenames.
var (
	startTime time.Time
	initOnce  sync.Once

	fileMu    sync.Mutex
	openFiles []*os.File
)

// trackFile registers a file for cleanup on Close.
func trackFile(f *os.File) {
	fileMu.Lock()
	openFiles = append(openFiles, f)
	fileMu.Unlock()
}

// Close syncs and closes every log file opened by this package.
// Call once during application shutdown (after all loggers have been Sync'd).
func Close() error {
	fileMu.Lock()
	defer fileMu.Unlock()

	var errs []error
	for _, f := range openFiles {
		if err := f.Sync(); err != nil {
			errs = append(errs, err)
		}
		if err := f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	openFiles = nil

	if len(errs) > 0 {
		return fmt.Errorf("closing log files: %v", errs)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Provider — main + module loggers with lifecycle management
// ---------------------------------------------------------------------------

// Provider holds the main application logger and category-specific loggers.
// Use NewProvider to create a fully configured set of loggers.
type Provider struct {
	main       *zap.Logger
	categories map[string]*zap.Logger
}

// NewProvider creates the runtime (main) logger plus a dedicated logger for
// every other predefined category (AllCategories), except those listed in
// cfg.Log.Suppress. Suppressed categories return a no-op logger from Get.
// The runtime category always aliases the main logger and cannot be suppressed.
// The returned Provider manages all file handles; call Close on shutdown.
func NewProvider(cfg *config.Config) (*Provider, error) {
	main, err := newLogger(cfg, string(CatRuntime))
	if err != nil {
		return nil, fmt.Errorf("create main logger: %w", err)
	}

	suppressed := make(map[string]bool, len(cfg.Log.Suppress))
	for _, s := range cfg.Log.Suppress {
		suppressed[strings.TrimSpace(s)] = true
	}

	cats := make(map[string]*zap.Logger, len(AllCategories()))
	for _, c := range AllCategories() {
		name := string(c)
		// runtime IS the main logger — never suppressed, never duplicated.
		if name == string(CatRuntime) {
			cats[name] = main
			continue
		}
		if suppressed[name] {
			cats[name] = zap.NewNop()
			continue
		}
		ml, err := newLogger(cfg, name)
		if err != nil {
			return nil, fmt.Errorf("create category logger %q: %w", name, err)
		}
		cats[name] = ml
	}

	return &Provider{main: main, categories: cats}, nil
}

// Main returns the main application logger.
func (p *Provider) Main() *zap.Logger { return p.main }

// Get returns the category-specific logger. If the category was listed in
// LOG_SUPPRESS, the returned logger is a no-op (logs are silently dropped).
// For unknown category names, the main logger is returned as fallback.
func (p *Provider) Get(name string) *zap.Logger {
	if ml, ok := p.categories[name]; ok {
		return ml
	}
	return p.main
}

// Close syncs all loggers and closes every log file managed by this provider.
func (p *Provider) Close() error {
	_ = p.main.Sync()
	for _, ml := range p.categories {
		_ = ml.Sync()
	}
	return Close()
}

// ---------------------------------------------------------------------------
// Standalone constructors (for tools like initdb)
// ---------------------------------------------------------------------------

// New creates the main (runtime) logger.
// Output goes to stdout and—if LOG_DIR is configured—to runtime-<timestamp>.log.
func New(cfg *config.Config) (*zap.Logger, error) {
	return newLogger(cfg, string(CatRuntime))
}

// NewModule creates a logger that writes to a module-specific file
// (<module>-<timestamp>.log) in addition to stdout.
func NewModule(cfg *config.Config, module string) (*zap.Logger, error) {
	return newLogger(cfg, module)
}

// ---------------------------------------------------------------------------
// Internal logger builder
// ---------------------------------------------------------------------------

func newLogger(cfg *config.Config, name string) (*zap.Logger, error) {
	initOnce.Do(func() { startTime = time.Now() })

	level := parseLevel(cfg.LogLevel)
	ts := startTime.Unix()

	var cores []zapcore.Core

	// Console core — stdout (human-readable in dev, JSON in prod).
	consoleEncoder := buildConsoleEncoder(cfg.AppEnv)
	cores = append(cores, zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level))

	// File core — structured JSON, no colour codes.
	if cfg.Log.Dir != "" {
		if err := os.MkdirAll(cfg.Log.Dir, 0o755); err != nil {
			return nil, fmt.Errorf("create log directory %q: %w", cfg.Log.Dir, err)
		}

		filename := filepath.Join(cfg.Log.Dir, fmt.Sprintf("%d.%s.log", ts, name))
		file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open log file %q: %w", filename, err)
		}
		trackFile(file)

		cores = append(cores, zapcore.NewCore(buildFileEncoder(), zapcore.AddSync(file), level))
	}

	opts := []zap.Option{zap.AddCaller()}
	if name != string(CatRuntime) {
		opts = append(opts, zap.Fields(zap.String("module", name)))
	}

	return zap.New(zapcore.NewTee(cores...), opts...), nil
}

// ---------------------------------------------------------------------------
// Encoder builders
// ---------------------------------------------------------------------------

func parseLevel(s string) zap.AtomicLevel {
	switch strings.ToLower(s) {
	case "debug":
		return zap.NewAtomicLevelAt(zap.DebugLevel)
	case "warn":
		return zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		return zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		return zap.NewAtomicLevelAt(zap.InfoLevel)
	}
}

// buildConsoleEncoder returns a console (human-readable) encoder for development
// and a JSON encoder for production.
func buildConsoleEncoder(env string) zapcore.Encoder {
	cfg := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	if env == "development" {
		cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		return zapcore.NewConsoleEncoder(cfg)
	}

	cfg.EncodeLevel = zapcore.CapitalLevelEncoder
	return zapcore.NewJSONEncoder(cfg)
}

// buildFileEncoder returns a JSON encoder suitable for file output — always
// structured, no colour codes, regardless of environment.
func buildFileEncoder() zapcore.Encoder {
	return zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
	})
}
