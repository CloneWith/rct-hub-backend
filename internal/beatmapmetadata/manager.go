// Package beatmapmetadata manages best-effort osu! beatmap data separately
// from the authoritative match aggregate.
package beatmapmetadata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"
)

type Status string

const (
	StatusPending Status = "PENDING"
	StatusReady   Status = "READY"
	StatusFailed  Status = "FAILED"
)

var (
	ErrRecordNotFound = errors.New("beatmap metadata record not found")
	ErrLeaseLost      = errors.New("beatmap metadata lease is no longer owned")
)

type Record struct {
	BeatmapID  int64
	Status     Status
	Attempts   int
	NextTryAt  time.Time
	LastError  string
	LeaseUntil *time.Time
	LeaseToken string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Store interface {
	Ensure(context.Context, int64, time.Time) error
	Get(context.Context, int64) (Record, error)
	Claim(context.Context, time.Time, time.Time) (*Record, error)
	MarkReady(context.Context, int64, string, time.Time) error
	Fail(context.Context, int64, string, string, time.Time, time.Time) error
	Retry(context.Context, int64, time.Time) error
}

type BeatmapRepository interface {
	ByOsuID(context.Context, int64) (*domain.Beatmap, error)
}

type Syncer interface {
	SyncBeatmap(context.Context, int64) (*domain.Beatmap, error)
}

type Manager struct {
	store    Store
	beatmaps BeatmapRepository
	syncer   Syncer
	now      func() time.Time
	lease    time.Duration
}

func New(store Store, beatmaps BeatmapRepository, syncer Syncer) *Manager {
	return &Manager{store: store, beatmaps: beatmaps, syncer: syncer, now: time.Now, lease: time.Minute}
}

// State returns a durable, user-visible metadata state without making a
// network request. The first miss schedules background resolution.
func (m *Manager) State(ctx context.Context, beatmapID int64) (Record, error) {
	if m == nil || m.store == nil || m.beatmaps == nil || beatmapID <= 0 {
		return Record{}, fmt.Errorf("beatmap metadata manager is not configured")
	}
	beatmap, err := m.beatmaps.ByOsuID(ctx, beatmapID)
	if err == nil && beatmap != nil {
		record, recordErr := m.store.Get(ctx, beatmapID)
		if recordErr == nil {
			// A cached beatmap can be stale. Preserve an existing pending or
			// failed refresh state until the background sync actually succeeds.
			return record, nil
		}
		if !errors.Is(recordErr, ErrRecordNotFound) {
			return Record{}, recordErr
		}
		now := m.now().UTC()
		if ensureErr := m.store.Ensure(ctx, beatmapID, now); ensureErr != nil {
			return Record{}, ensureErr
		}
		record, recordErr = m.store.Get(ctx, beatmapID)
		if recordErr != nil {
			return Record{}, recordErr
		}
		// A failed refresh is a meaningful degraded state even when an older
		// cached Beatmap document is still available to render.
		if record.LeaseToken != "" || record.Status == StatusReady || record.Status == StatusFailed {
			return record, nil
		}
		if readyErr := m.store.MarkReady(ctx, beatmapID, "", now); readyErr != nil {
			if errors.Is(readyErr, ErrLeaseLost) {
				return m.store.Get(ctx, beatmapID)
			}
			return Record{}, readyErr
		}
		return m.store.Get(ctx, beatmapID)
	}
	if err != nil && !errors.Is(err, errs.ErrNotFound) {
		return Record{}, fmt.Errorf("read cached beatmap metadata: %w", err)
	}
	record, statusErr := m.store.Get(ctx, beatmapID)
	if statusErr == nil {
		return record, nil
	}
	if !errors.Is(statusErr, ErrRecordNotFound) {
		return Record{}, statusErr
	}
	now := m.now().UTC()
	if err := m.store.Ensure(ctx, beatmapID, now); err != nil {
		return Record{}, err
	}
	return m.store.Get(ctx, beatmapID)
}

// Beatmap reads only already-persisted metadata; GraphQL reads never wait on
// the external osu! API.
func (m *Manager) Beatmap(ctx context.Context, beatmapID int64) (*domain.Beatmap, error) {
	if m == nil || m.beatmaps == nil || beatmapID <= 0 {
		return nil, nil
	}
	if _, err := m.State(ctx, beatmapID); err != nil {
		return nil, err
	}
	beatmap, err := m.beatmaps.ByOsuID(ctx, beatmapID)
	if errors.Is(err, errs.ErrNotFound) {
		return nil, nil
	}
	return beatmap, err
}

func (m *Manager) Retry(ctx context.Context, beatmapID int64) error {
	if m == nil || m.store == nil || beatmapID <= 0 {
		return fmt.Errorf("beatmap metadata manager is not configured")
	}
	return m.store.Retry(ctx, beatmapID, m.now().UTC())
}

// RunOnce resolves one due item. Failures remain visible and use bounded
// exponential retry; no match command or snapshot is touched.
func (m *Manager) RunOnce(ctx context.Context) error {
	if m == nil || m.store == nil || m.syncer == nil {
		return fmt.Errorf("beatmap metadata worker is not configured")
	}
	now := m.now().UTC()
	record, err := m.store.Claim(ctx, now, now.Add(m.lease))
	if err != nil || record == nil {
		return err
	}
	if _, err := m.syncer.SyncBeatmap(ctx, record.BeatmapID); err != nil {
		retryAt := now.Add(backoff(record.Attempts + 1))
		if failErr := m.store.Fail(ctx, record.BeatmapID, record.LeaseToken, err.Error(), retryAt, now); failErr != nil {
			return errors.Join(err, failErr)
		}
		return nil
	}
	return m.store.MarkReady(ctx, record.BeatmapID, record.LeaseToken, now)
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Minute
	for i := 1; i < attempt && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}
