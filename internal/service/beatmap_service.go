package service

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/paginate"
)

// BeatmapService handles beatmap management operations.
type BeatmapService struct {
	beatmaps    repository.BeatmapRepository
	invalidator CacheInvalidator
}

// NewBeatmapService creates a new BeatmapService. If invalidator is nil,
// a no-op implementation is used.
func NewBeatmapService(beatmaps repository.BeatmapRepository, invalidator CacheInvalidator) *BeatmapService {
	if invalidator == nil {
		invalidator = noopInvalidator{}
	}
	return &BeatmapService{beatmaps: beatmaps, invalidator: invalidator}
}

// Get returns a beatmap by id.
func (s *BeatmapService) Get(ctx context.Context, id bson.ObjectID) (*domain.Beatmap, error) {
	return s.beatmaps.ByID(ctx, id)
}

// GetByOsuID returns a beatmap by osu! beatmap id.
func (s *BeatmapService) GetByOsuID(ctx context.Context, osuID int64) (*domain.Beatmap, error) {
	return s.beatmaps.ByOsuID(ctx, osuID)
}

// List returns a paginated list of beatmaps.
func (s *BeatmapService) List(ctx context.Context, params paginate.Params) (paginate.Result[domain.Beatmap], error) {
	return s.beatmaps.List(ctx, params)
}

// Create creates a new beatmap entry.
func (s *BeatmapService) Create(ctx context.Context, beatmap *domain.Beatmap) error {
	if err := s.beatmaps.Create(ctx, beatmap); err != nil {
		return err
	}
	// New entry — invalidate in case a stale "not found" sentinel was cached.
	_ = s.invalidator.InvalidateBeatmap(ctx, beatmap.OnlineID)
	return nil
}

// Update updates an existing beatmap.
func (s *BeatmapService) Update(ctx context.Context, beatmap *domain.Beatmap) error {
	if err := s.beatmaps.Update(ctx, beatmap); err != nil {
		return err
	}
	// Invalidate cached copy so local-only field changes are visible.
	_ = s.invalidator.InvalidateBeatmap(ctx, beatmap.OnlineID)
	return nil
}

// Delete removes a beatmap by id.
func (s *BeatmapService) Delete(ctx context.Context, id bson.ObjectID) error {
	// Fetch first to obtain the osu! ID for cache invalidation.
	bm, err := s.beatmaps.ByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.beatmaps.Delete(ctx, id); err != nil {
		return err
	}
	// Invalidate cached copy so the deleted beatmap is not served from cache.
	_ = s.invalidator.InvalidateBeatmap(ctx, bm.OnlineID)
	return nil
}
