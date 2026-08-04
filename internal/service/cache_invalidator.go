package service

import "context"

// CacheInvalidator invalidates Redis cache entries after a user or beatmap
// write. fetcher.Fetcher implements this interface.
type CacheInvalidator interface {
	InvalidateUser(ctx context.Context, osuID int64) error
	InvalidateBeatmap(ctx context.Context, osuID int64) error
}

// noopInvalidator is a CacheInvalidator that does nothing. Used when no
// fetcher is wired (e.g., in unit tests that don't exercise caching).
type noopInvalidator struct{}

func (noopInvalidator) InvalidateUser(context.Context, int64) error    { return nil }
func (noopInvalidator) InvalidateBeatmap(context.Context, int64) error { return nil }
