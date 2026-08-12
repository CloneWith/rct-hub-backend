package beatmapmetadata

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"
)

type memoryStore struct{ records map[int64]Record }

func newMemoryStore() *memoryStore { return &memoryStore{records: map[int64]Record{}} }
func (s *memoryStore) Ensure(_ context.Context, id int64, now time.Time) error {
	if _, ok := s.records[id]; !ok {
		s.records[id] = Record{BeatmapID: id, Status: StatusPending, NextTryAt: now, CreatedAt: now, UpdatedAt: now}
	}
	return nil
}
func (s *memoryStore) Get(_ context.Context, id int64) (Record, error) {
	record, ok := s.records[id]
	if !ok {
		return Record{}, ErrRecordNotFound
	}
	return record, nil
}
func (s *memoryStore) Claim(_ context.Context, now, lease time.Time) (*Record, error) {
	for id, record := range s.records {
		if (record.Status == StatusPending || record.Status == StatusFailed) && !record.NextTryAt.After(now) && (record.LeaseUntil == nil || !record.LeaseUntil.After(now)) {
			record.Status, record.LeaseUntil, record.LeaseToken = StatusPending, &lease, fmt.Sprintf("lease-%d", id)
			s.records[id] = record
			return &record, nil
		}
	}
	return nil, nil
}
func (s *memoryStore) MarkReady(_ context.Context, id int64, leaseToken string, now time.Time) error {
	record := s.records[id]
	if leaseToken != "" && record.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	record.Status, record.LastError, record.LeaseUntil, record.UpdatedAt = StatusReady, "", nil, now
	record.LeaseToken = ""
	s.records[id] = record
	return nil
}
func (s *memoryStore) Fail(_ context.Context, id int64, leaseToken, message string, retryAt, now time.Time) error {
	record := s.records[id]
	if record.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	record.Status, record.LastError, record.NextTryAt, record.LeaseUntil, record.UpdatedAt = StatusFailed, message, retryAt, nil, now
	record.LeaseToken = ""
	record.Attempts++
	s.records[id] = record
	return nil
}
func (s *memoryStore) Retry(_ context.Context, id int64, now time.Time) error {
	record, ok := s.records[id]
	if !ok {
		return ErrRecordNotFound
	}
	record.Status, record.NextTryAt, record.LastError, record.LeaseUntil = StatusPending, now, "", nil
	record.LeaseToken = ""
	s.records[id] = record
	return nil
}

type beatmapRepoStub struct{ beatmap *domain.Beatmap }

func (s beatmapRepoStub) ByOsuID(context.Context, int64) (*domain.Beatmap, error) {
	if s.beatmap == nil {
		return nil, errs.ErrNotFound
	}
	return s.beatmap, nil
}

type syncerStub struct {
	err   error
	calls int
}

func (s *syncerStub) SyncBeatmap(_ context.Context, id int64) (*domain.Beatmap, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &domain.Beatmap{OnlineID: id}, nil
}

func TestStateSchedulesMissWithoutCallingExternalAPI(t *testing.T) {
	store, syncer := newMemoryStore(), &syncerStub{}
	manager := New(store, beatmapRepoStub{}, syncer)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	record, err := manager.State(context.Background(), 123)
	if err != nil || record.Status != StatusPending || syncer.calls != 0 {
		t.Fatalf("record=%+v calls=%d err=%v", record, syncer.calls, err)
	}
}

func TestWorkerPersistsFailureThenManualRetryAndReady(t *testing.T) {
	store, syncer := newMemoryStore(), &syncerStub{err: errors.New("osu unavailable")}
	manager := New(store, beatmapRepoStub{}, syncer)
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	_, _ = manager.State(context.Background(), 123)
	if err := manager.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, _ := store.Get(context.Background(), 123)
	if failed.Status != StatusFailed || failed.Attempts != 1 || failed.LastError != "osu unavailable" || !failed.NextTryAt.After(now) {
		t.Fatalf("failed=%+v", failed)
	}
	if err := manager.Retry(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
	syncer.err = nil
	if err := manager.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	ready, _ := store.Get(context.Background(), 123)
	if ready.Status != StatusReady || syncer.calls != 2 {
		t.Fatalf("ready=%+v calls=%d", ready, syncer.calls)
	}
}

func TestCachedBeatmapIsImmediatelyReady(t *testing.T) {
	store := newMemoryStore()
	manager := New(store, beatmapRepoStub{beatmap: &domain.Beatmap{OnlineID: 123}}, &syncerStub{})
	record, err := manager.State(context.Background(), 123)
	if err != nil || record.Status != StatusReady {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}

func TestCachedBeatmapDoesNotStealActiveRefreshLease(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	leaseUntil := now.Add(time.Minute)
	store.records[123] = Record{BeatmapID: 123, Status: StatusPending, NextTryAt: now, LeaseUntil: &leaseUntil, LeaseToken: "worker-lease"}
	manager := New(store, beatmapRepoStub{beatmap: &domain.Beatmap{OnlineID: 123}}, &syncerStub{})
	manager.now = func() time.Time { return now }
	record, err := manager.State(context.Background(), 123)
	if err != nil || record.Status != StatusPending || record.LeaseToken != "worker-lease" {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}

func TestBeatmapReadSchedulesBackgroundResolution(t *testing.T) {
	store, syncer := newMemoryStore(), &syncerStub{}
	manager := New(store, beatmapRepoStub{}, syncer)
	beatmap, err := manager.Beatmap(context.Background(), 321)
	if err != nil || beatmap != nil || syncer.calls != 0 {
		t.Fatalf("beatmap=%+v calls=%d err=%v", beatmap, syncer.calls, err)
	}
	record, err := store.Get(context.Background(), 321)
	if err != nil || record.Status != StatusPending {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}
