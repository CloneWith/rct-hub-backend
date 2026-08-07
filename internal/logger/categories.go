package logger

// ---------------------------------------------------------------------------
// Log categories — predefined module names for structured per-file logging.
//
// These constants are NOT wired into every call-site yet. They exist so that
// future refactors can replace ad-hoc string literals with typed references,
// and so the set of sanctioned categories is discoverable in one place.
//
// Usage:
//
//	log := provider.Get(logger.CatStorage)
//	log.Info("connected", zap.String("db", "rcthub"))
//
// By default, every category gets its own log file. The "runtime" category IS
// the main logger (Provider.Main / logger.New) — it is always active and
// cannot be suppressed. To silence any other category, list it in the
// LOG_SUPPRESS environment variable (comma-separated).
// ---------------------------------------------------------------------------

// Category is a typed log module name.
type Category string

const (
	// CatRuntime — general application lifecycle: server start/stop, config
	// load, graceful shutdown, background workers. This IS the main logger
	// returned by Provider.Main(); it is always active and cannot be suppressed.
	CatRuntime Category = "runtime"

	// CatStorage — MongoDB and Redis operations: connection management,
	// query execution, cache hits/misses, index checks, replica-set health.
	CatStorage Category = "storage"

	// CatNetwork — inbound HTTP request tracing (Gin access logs) and
	// outbound calls: osu! API fetcher, webhook deliveries, upstream
	// proxies. Use for latency, status codes, and retry diagnostics.
	CatNetwork Category = "network"

	// CatAuth — user authentication and session lifecycle: osu! OAuth
	// handshake, JWT issuance/validation/refresh, token revocation,
	// session expiry.
	CatAuth Category = "auth"

	// CatAudit — security-sensitive state mutations that must be
	// traceable after the fact: role grants/revocations, ban/unban,
	// verification status changes, match result finalisation, and any
	// admin action that modifies user identity or match outcome.
	CatAudit Category = "audit"

	// CatMatchEngine — the deterministic rules engine: board transitions,
	// command execution, event emission, timer state changes, win
	// detection, and robbery logic.
	CatMatchEngine Category = "match-engine"

	// CatFetcher — the osu! API proxy fetcher: three-tier cache lookups
	// (Redis → MongoDB → osu! API v2), token refresh, cache invalidation.
	CatFetcher Category = "fetcher"
)

// AllCategories returns every predefined category in declaration order.
// Useful for validation, documentation generation, or seeding default
// LOG_SUPPRESS values.
func AllCategories() []Category {
	return []Category{
		CatRuntime,
		CatStorage,
		CatNetwork,
		CatAuth,
		CatAudit,
		CatMatchEngine,
		CatFetcher,
	}
}

// CategoryNames returns the string values of all predefined categories.
// Convenience wrapper around AllCategories for places that need plain strings.
func CategoryNames() []string {
	cats := AllCategories()
	names := make([]string, len(cats))
	for i, c := range cats {
		names[i] = string(c)
	}
	return names
}
