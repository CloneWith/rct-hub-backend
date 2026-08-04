package service

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
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
	// Invalidate any stale entry left from an earlier lifecycle of this osu! ID.
	if err := s.invalidator.InvalidateBeatmap(ctx, beatmap.OnlineID); err != nil {
		return fmt.Errorf("%w: create beatmap: %w", errs.ErrCacheSync, err)
	}
	return nil
}

// Update updates an existing beatmap.
func (s *BeatmapService) Update(ctx context.Context, beatmap *domain.Beatmap) error {
	stored, err := s.beatmaps.ByID(ctx, beatmap.ID)
	if err != nil {
		return err
	}
	if beatmap.OnlineID != stored.OnlineID {
		return fmt.Errorf("%w: beatmap osu! id cannot be changed", errs.ErrInvalidInput)
	}
	if err := s.beatmaps.Update(ctx, beatmap); err != nil {
		return err
	}
	// Invalidate cached copy so local-only field changes are visible.
	if err := s.invalidator.InvalidateBeatmap(ctx, beatmap.OnlineID); err != nil {
		return fmt.Errorf("%w: update beatmap: %w", errs.ErrCacheSync, err)
	}
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
	if err := s.invalidator.InvalidateBeatmap(ctx, bm.OnlineID); err != nil {
		return fmt.Errorf("%w: delete beatmap: %w", errs.ErrCacheSync, err)
	}
	return nil
}
