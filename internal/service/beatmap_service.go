package service

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// BeatmapService handles beatmap management operations.
type BeatmapService struct {
	beatmaps    repository.BeatmapRepository
	invalidator CacheInvalidator
	log         *zap.Logger
}

// NewBeatmapService creates a new BeatmapService. If invalidator is nil,
// a no-op implementation is used.
func NewBeatmapService(beatmaps repository.BeatmapRepository, invalidator CacheInvalidator, log *zap.Logger) *BeatmapService {
	if invalidator == nil {
		invalidator = noopInvalidator{}
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &BeatmapService{beatmaps: beatmaps, invalidator: invalidator, log: log}
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
		s.log.Error("failed to create beatmap", zap.Error(err))
		return err
	}
	// Invalidate any stale entry left from an earlier lifecycle of this osu! ID.
	if err := s.invalidator.InvalidateBeatmap(ctx, beatmap.OnlineID); err != nil {
		s.log.Error("failed to invalidate beatmap cache", zap.Int64("osu_id", beatmap.OnlineID), zap.Error(err))
		return fmt.Errorf("%w: create beatmap: %w", errs.ErrCacheSync, err)
	}
	s.log.Debug("beatmap created in db", zap.Int64("osu_id", beatmap.OnlineID))
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
		s.log.Error("failed to update beatmap", zap.Error(err))
		return err
	}
	// Invalidate cached copy so local-only field changes are visible.
	if err := s.invalidator.InvalidateBeatmap(ctx, beatmap.OnlineID); err != nil {
		s.log.Error("failed to invalidate beatmap cache", zap.Int64("osu_id", beatmap.OnlineID), zap.Error(err))
		return fmt.Errorf("%w: update beatmap: %w", errs.ErrCacheSync, err)
	}
	s.log.Debug("beatmap updated in db", zap.Int64("osu_id", beatmap.OnlineID))
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
		s.log.Error("failed to delete beatmap", zap.Error(err))
		return err
	}
	// Invalidate cached copy so the deleted beatmap is not served from cache.
	if err := s.invalidator.InvalidateBeatmap(ctx, bm.OnlineID); err != nil {
		s.log.Error("failed to invalidate beatmap cache", zap.Int64("osu_id", bm.OnlineID), zap.Error(err))
		return fmt.Errorf("%w: delete beatmap: %w", errs.ErrCacheSync, err)
	}
	s.log.Debug("beatmap deleted from db", zap.Int64("osu_id", bm.OnlineID))
	return nil
}
