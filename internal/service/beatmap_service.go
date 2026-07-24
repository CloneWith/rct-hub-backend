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
	beatmaps repository.BeatmapRepository
}

// NewBeatmapService creates a new BeatmapService.
func NewBeatmapService(beatmaps repository.BeatmapRepository) *BeatmapService {
	return &BeatmapService{beatmaps: beatmaps}
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
	return s.beatmaps.Create(ctx, beatmap)
}

// Update updates an existing beatmap.
func (s *BeatmapService) Update(ctx context.Context, beatmap *domain.Beatmap) error {
	return s.beatmaps.Update(ctx, beatmap)
}

// Delete removes a beatmap by id.
func (s *BeatmapService) Delete(ctx context.Context, id bson.ObjectID) error {
	return s.beatmaps.Delete(ctx, id)
}
