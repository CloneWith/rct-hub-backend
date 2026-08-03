package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
)

// Sentinel errors specific to the fetcher.
var (
	// ErrNotFound is returned when the osu! API responds with 404.
	ErrNotFound = fmt.Errorf("osu! resource not found")
)

// Default cache TTLs.
const (
	defaultUserCacheTTL    = 30 * time.Minute
	defaultBeatmapCacheTTL = 24 * time.Hour
)

// Fetcher is the entry point for on-demand osu! data retrieval.
// It implements a three-tier lookup: Redis hot cache → MongoDB persistent
// store → osu! API v2 live fetch. Data fetched from the API is persisted
// back to MongoDB and cached in Redis.
type Fetcher interface {
	// GetUser returns a user by osu! ID. It first checks Redis, then
	// MongoDB; on miss it fetches from the osu! API and persists.
	GetUser(ctx context.Context, osuID int64) (*domain.User, error)

	// SyncUser forces a fresh fetch from the osu! API and updates the
	// database. Local-only fields (Roles, VerifyStatus, IsBanned) are
	// preserved.
	SyncUser(ctx context.Context, osuID int64) (*domain.User, error)

	// GetBeatmap returns a beatmap by osu! ID. Same cache strategy as
	// GetUser.
	GetBeatmap(ctx context.Context, osuID int64) (*domain.Beatmap, error)

	// SyncBeatmap forces a fresh fetch from the osu! API and updates the
	// database. Local-only extended fields (ModString, ModIndex, etc.)
	// are preserved.
	SyncBeatmap(ctx context.Context, osuID int64) (*domain.Beatmap, error)
}

// fetcher is the default Fetcher implementation.
type fetcher struct {
	api      *APIClient
	users    repository.UserRepository
	beatmaps repository.BeatmapRepository
	rdb      *redis.Client
	logger   *zap.Logger

	userCacheTTL    time.Duration
	beatmapCacheTTL time.Duration
}

// Config holds optional configuration for the fetcher.
type Config struct {
	UserCacheTTL    time.Duration
	BeatmapCacheTTL time.Duration
}

// New creates a new Fetcher.
func New(api *APIClient, users repository.UserRepository, beatmaps repository.BeatmapRepository, rdb *redis.Client, logger *zap.Logger, cfg Config) Fetcher {
	if cfg.UserCacheTTL == 0 {
		cfg.UserCacheTTL = defaultUserCacheTTL
	}
	if cfg.BeatmapCacheTTL == 0 {
		cfg.BeatmapCacheTTL = defaultBeatmapCacheTTL
	}
	return &fetcher{
		api:             api,
		users:           users,
		beatmaps:        beatmaps,
		rdb:             rdb,
		logger:          logger,
		userCacheTTL:    cfg.UserCacheTTL,
		beatmapCacheTTL: cfg.BeatmapCacheTTL,
	}
}

// --- User methods ---

// GetUser implements Fetcher.
func (f *fetcher) GetUser(ctx context.Context, osuID int64) (*domain.User, error) {
	// 1. Redis hot cache.
	if user, ok := f.getCachedUser(ctx, osuID); ok {
		return user, nil
	}

	// 2. MongoDB persistent store.
	user, err := f.users.ByOsuID(ctx, osuID)
	if err == nil {
		f.cacheUser(ctx, user)
		return user, nil
	}
	if !errors.Is(err, errs.ErrNotFound) {
		return nil, fmt.Errorf("fetch user from db: %w", err)
	}

	// 3. osu! API fallback.
	return f.SyncUser(ctx, osuID)
}

// SyncUser implements Fetcher.
func (f *fetcher) SyncUser(ctx context.Context, osuID int64) (*domain.User, error) {
	resp, err := f.api.GetUser(ctx, osuID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: osu! user %d", errs.ErrNotFound, osuID)
		}
		return nil, fmt.Errorf("fetch user from osu! api: %w", err)
	}

	// Try to load the existing user so we can preserve local-only fields.
	existing, err := f.users.ByOsuID(ctx, osuID)
	if err != nil && !errors.Is(err, errs.ErrNotFound) {
		return nil, fmt.Errorf("check existing user: %w", err)
	}

	user := mergeUser(existing, resp)

	if existing != nil {
		if err := f.users.Update(ctx, user); err != nil {
			return nil, fmt.Errorf("update user: %w", err)
		}
	} else {
		if err := f.users.Create(ctx, user); err != nil {
			if errors.Is(err, errs.ErrAlreadyExists) {
				// Race condition: another request created it first — update instead.
				if err := f.users.Update(ctx, user); err != nil {
					return nil, fmt.Errorf("update user after race: %w", err)
				}
			} else {
				return nil, fmt.Errorf("create user: %w", err)
			}
		}
	}

	f.cacheUser(ctx, user)
	return user, nil
}

// --- Beatmap methods ---

// GetBeatmap implements Fetcher.
func (f *fetcher) GetBeatmap(ctx context.Context, osuID int64) (*domain.Beatmap, error) {
	// 1. Redis hot cache.
	if bm, ok := f.getCachedBeatmap(ctx, osuID); ok {
		return bm, nil
	}

	// 2. MongoDB persistent store.
	bm, err := f.beatmaps.ByOsuID(ctx, osuID)
	if err == nil {
		f.cacheBeatmap(ctx, bm)
		return bm, nil
	}
	if !errors.Is(err, errs.ErrNotFound) {
		return nil, fmt.Errorf("fetch beatmap from db: %w", err)
	}

	// 3. osu! API fallback.
	return f.SyncBeatmap(ctx, osuID)
}

// SyncBeatmap implements Fetcher.
func (f *fetcher) SyncBeatmap(ctx context.Context, osuID int64) (*domain.Beatmap, error) {
	resp, err := f.api.GetBeatmap(ctx, osuID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: osu! beatmap %d", errs.ErrNotFound, osuID)
		}
		return nil, fmt.Errorf("fetch beatmap from osu! api: %w", err)
	}

	// Try to load the existing beatmap so we can preserve local-only fields.
	existing, err := f.beatmaps.ByOsuID(ctx, osuID)
	if err != nil && !errors.Is(err, errs.ErrNotFound) {
		return nil, fmt.Errorf("check existing beatmap: %w", err)
	}

	bm := mergeBeatmap(existing, resp)

	if existing != nil {
		if err := f.beatmaps.Update(ctx, bm); err != nil {
			return nil, fmt.Errorf("update beatmap: %w", err)
		}
	} else {
		if err := f.beatmaps.Create(ctx, bm); err != nil {
			if errors.Is(err, errs.ErrAlreadyExists) {
				if err := f.beatmaps.Update(ctx, bm); err != nil {
					return nil, fmt.Errorf("update beatmap after race: %w", err)
				}
			} else {
				return nil, fmt.Errorf("create beatmap: %w", err)
			}
		}
	}

	f.cacheBeatmap(ctx, bm)
	return bm, nil
}

// --- Cache helpers ---

func userCacheKey(osuID int64) string {
	return fmt.Sprintf("fetcher:user:%d", osuID)
}

func beatmapCacheKey(osuID int64) string {
	return fmt.Sprintf("fetcher:beatmap:%d", osuID)
}

func (f *fetcher) getCachedUser(ctx context.Context, osuID int64) (*domain.User, bool) {
	data, err := f.rdb.Get(ctx, userCacheKey(osuID)).Bytes()
	if err != nil {
		return nil, false
	}
	var user domain.User
	if err := json.Unmarshal(data, &user); err != nil {
		f.logger.Warn("failed to unmarshal cached user", zap.Int64("osu_id", osuID), zap.Error(err))
		return nil, false
	}
	return &user, true
}

func (f *fetcher) cacheUser(ctx context.Context, user *domain.User) {
	data, err := json.Marshal(user)
	if err != nil {
		f.logger.Warn("failed to marshal user for cache", zap.Int64("osu_id", user.OnlineID), zap.Error(err))
		return
	}
	if err := f.rdb.Set(ctx, userCacheKey(user.OnlineID), data, f.userCacheTTL).Err(); err != nil {
		f.logger.Warn("failed to cache user in redis", zap.Int64("osu_id", user.OnlineID), zap.Error(err))
	}
}

func (f *fetcher) getCachedBeatmap(ctx context.Context, osuID int64) (*domain.Beatmap, bool) {
	data, err := f.rdb.Get(ctx, beatmapCacheKey(osuID)).Bytes()
	if err != nil {
		return nil, false
	}
	var bm domain.Beatmap
	if err := json.Unmarshal(data, &bm); err != nil {
		f.logger.Warn("failed to unmarshal cached beatmap", zap.Int64("osu_id", osuID), zap.Error(err))
		return nil, false
	}
	return &bm, true
}

func (f *fetcher) cacheBeatmap(ctx context.Context, bm *domain.Beatmap) {
	data, err := json.Marshal(bm)
	if err != nil {
		f.logger.Warn("failed to marshal beatmap for cache", zap.Int64("osu_id", bm.OnlineID), zap.Error(err))
		return
	}
	if err := f.rdb.Set(ctx, beatmapCacheKey(bm.OnlineID), data, f.beatmapCacheTTL).Err(); err != nil {
		f.logger.Warn("failed to cache beatmap in redis", zap.Int64("osu_id", bm.OnlineID), zap.Error(err))
	}
}

// --- Merge helpers ---

// mergeUser applies osu! API data to an existing user (or creates a new one),
// preserving local-only fields such as Roles, VerifyStatus, and IsBanned.
func mergeUser(existing *domain.User, resp *OsuUserResponse) *domain.User {
	if existing == nil {
		return &domain.User{
			OnlineID:     resp.ID,
			Username:     resp.Username,
			AvatarURL:    resp.AvatarURL,
			CountryCode:  resp.Country.Code,
			GlobalRank:   resp.Statistics.GlobalRank,
			PP:           resp.Statistics.PP,
			Roles:        []domain.UserRole{domain.RolePlayer},
			VerifyStatus: domain.Pending,
		}
	}
	existing.Username = resp.Username
	existing.AvatarURL = resp.AvatarURL
	existing.CountryCode = resp.Country.Code
	existing.GlobalRank = resp.Statistics.GlobalRank
	existing.PP = resp.Statistics.PP
	return existing
}

// mergeBeatmap applies osu! API data to an existing beatmap (or creates a new
// one), preserving local-only extended fields.
func mergeBeatmap(existing *domain.Beatmap, resp *OsuBeatmapResponse) *domain.Beatmap {
	if existing == nil {
		return &domain.Beatmap{
			OnlineID:          resp.ID,
			BeatmapsetID:      resp.BeatmapsetID,
			Title:             resp.Beatmapset.Title,
			Artist:            resp.Beatmapset.Artist,
			DifficultyName:    resp.Version,
			AuthorID:          resp.UserID,
			RulesetID:         resp.ModeInt,
			Status:            resp.Status,
			StarRating:        resp.DifficultyRating,
			BPM:               resp.BPM,
			TotalLength:       resp.TotalLength,
			DrainRate:         resp.Drain,
			CircleSize:        resp.CS,
			ApproachRate:      resp.AR,
			OverallDifficulty: resp.Accuracy,
			CoverURL:          resp.Beatmapset.Covers.Cover,
		}
	}
	existing.BeatmapsetID = resp.BeatmapsetID
	existing.Title = resp.Beatmapset.Title
	existing.Artist = resp.Beatmapset.Artist
	existing.DifficultyName = resp.Version
	existing.AuthorID = resp.UserID
	existing.RulesetID = resp.ModeInt
	existing.Status = resp.Status
	existing.StarRating = resp.DifficultyRating
	existing.BPM = resp.BPM
	existing.TotalLength = resp.TotalLength
	existing.DrainRate = resp.Drain
	existing.CircleSize = resp.CS
	existing.ApproachRate = resp.AR
	existing.OverallDifficulty = resp.Accuracy
	existing.CoverURL = resp.Beatmapset.Covers.Cover
	return existing
}
