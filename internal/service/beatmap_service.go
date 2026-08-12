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
	resolver    interface {
		GetBeatmap(context.Context, int64) (*domain.Beatmap, error)
	}
	log *zap.Logger
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
	service := &BeatmapService{beatmaps: beatmaps, invalidator: invalidator, log: log}
	if resolver, ok := invalidator.(interface {
		GetBeatmap(context.Context, int64) (*domain.Beatmap, error)
	}); ok {
		service.resolver = resolver
	}
	return service
}

// Get returns a beatmap by id.
func (s *BeatmapService) Get(ctx context.Context, id bson.ObjectID) (*domain.Beatmap, error) {
	return s.beatmaps.ByID(ctx, id)
}

// GetByOsuID returns a beatmap by osu! beatmap id.
func (s *BeatmapService) GetByOsuID(ctx context.Context, osuID int64) (*domain.Beatmap, error) {
	if s.resolver != nil {
		return s.resolver.GetBeatmap(ctx, osuID)
	}
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

// BeatmapPatch is a partial update request for a beatmap. Only non-nil fields
// are applied; omitted fields keep their current values.
type BeatmapPatch struct {
	BeatmapsetID      *int64   `json:"beatmapset_id,omitempty"`
	Title             *string  `json:"title,omitempty"`
	Artist            *string  `json:"artist,omitempty"`
	DifficultyName    *string  `json:"version,omitempty"`
	AuthorID          *int64   `json:"user_id,omitempty"`
	RulesetID         *int     `json:"mode_int,omitempty"`
	Status            *string  `json:"status,omitempty"`
	StarRating        *float64 `json:"difficulty_rating,omitempty"`
	BPM               *float64 `json:"bpm,omitempty"`
	TotalLength       *int     `json:"total_length,omitempty"`
	DrainRate         *float64 `json:"drain,omitempty"`
	CircleSize        *float64 `json:"cs,omitempty"`
	ApproachRate      *float64 `json:"ar,omitempty"`
	OverallDifficulty *float64 `json:"accuracy,omitempty"`
	CoverURL          *string  `json:"cover_url,omitempty"`
	ModString         *string  `json:"mod_string,omitempty"`
	ModIndex          *int     `json:"mod_index,omitempty"`
	SelectorID        *int64   `json:"selector_id,omitempty"`
	CreditUserIDs     *[]int64 `json:"credit_user_ids,omitempty"`
	Skill             *string  `json:"skill,omitempty"`
	Comment           *string  `json:"comment,omitempty"`
	IsOriginal        *bool    `json:"is_original,omitempty"`
}

// Patch applies a partial update to an existing beatmap. The beatmap osu! id
// (OnlineID) is immutable and never taken from the patch.
func (s *BeatmapService) Patch(ctx context.Context, id bson.ObjectID, patch *BeatmapPatch) (*domain.Beatmap, error) {
	bm, err := s.beatmaps.ByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if patch.BeatmapsetID != nil {
		bm.BeatmapsetID = *patch.BeatmapsetID
	}
	if patch.Title != nil {
		bm.Title = *patch.Title
	}
	if patch.Artist != nil {
		bm.Artist = *patch.Artist
	}
	if patch.DifficultyName != nil {
		bm.DifficultyName = *patch.DifficultyName
	}
	if patch.AuthorID != nil {
		bm.AuthorID = *patch.AuthorID
	}
	if patch.RulesetID != nil {
		bm.RulesetID = *patch.RulesetID
	}
	if patch.Status != nil {
		bm.Status = *patch.Status
	}
	if patch.StarRating != nil {
		bm.StarRating = *patch.StarRating
	}
	if patch.BPM != nil {
		bm.BPM = *patch.BPM
	}
	if patch.TotalLength != nil {
		bm.TotalLength = *patch.TotalLength
	}
	if patch.DrainRate != nil {
		bm.DrainRate = *patch.DrainRate
	}
	if patch.CircleSize != nil {
		bm.CircleSize = *patch.CircleSize
	}
	if patch.ApproachRate != nil {
		bm.ApproachRate = *patch.ApproachRate
	}
	if patch.OverallDifficulty != nil {
		bm.OverallDifficulty = *patch.OverallDifficulty
	}
	if patch.CoverURL != nil {
		bm.CoverURL = *patch.CoverURL
	}
	if patch.ModString != nil {
		bm.ModString = *patch.ModString
	}
	if patch.ModIndex != nil {
		bm.ModIndex = *patch.ModIndex
	}
	if patch.SelectorID != nil {
		bm.SelectorID = *patch.SelectorID
	}
	if patch.CreditUserIDs != nil {
		bm.CreditUserIDs = *patch.CreditUserIDs
	}
	if patch.Skill != nil {
		bm.Skill = *patch.Skill
	}
	if patch.Comment != nil {
		bm.Comment = *patch.Comment
	}
	if patch.IsOriginal != nil {
		bm.IsOriginal = *patch.IsOriginal
	}

	if err := s.beatmaps.Update(ctx, bm); err != nil {
		s.log.Error("failed to patch beatmap", zap.String("id", id.Hex()), zap.Error(err))
		return nil, err
	}
	if err := s.invalidator.InvalidateBeatmap(ctx, bm.OnlineID); err != nil {
		s.log.Error("failed to invalidate beatmap cache", zap.Int64("osu_id", bm.OnlineID), zap.Error(err))
		return nil, fmt.Errorf("%w: patch beatmap: %w", errs.ErrCacheSync, err)
	}
	s.log.Debug("beatmap patched in db", zap.Int64("osu_id", bm.OnlineID))
	return bm, nil
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
