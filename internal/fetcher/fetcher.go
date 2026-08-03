package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
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

	// SyncUser forces a fresh fetch from the osu! API and atomically
	// upserts the API-owned fields into MongoDB. Local-only fields
	// (Roles, VerifyStatus, IsBanned) are never touched by this operation.
	SyncUser(ctx context.Context, osuID int64) (*domain.User, error)

	// GetBeatmap returns a beatmap by osu! ID. Same cache strategy as
	// GetUser.
	GetBeatmap(ctx context.Context, osuID int64) (*domain.Beatmap, error)

	// SyncBeatmap forces a fresh fetch from the osu! API and atomically
	// upserts the API-owned fields into MongoDB. Local-only extended
	// fields (ModString, ModIndex, etc.) are never touched.
	SyncBeatmap(ctx context.Context, osuID int64) (*domain.Beatmap, error)

	// InvalidateUser removes the cached user from Redis. Call this when
	// local-only fields (Roles, VerifyStatus, IsBanned) are modified by
	// admin write paths to prevent stale data from being served.
	InvalidateUser(ctx context.Context, osuID int64) error

	// InvalidateBeatmap removes the cached beatmap from Redis.
	InvalidateBeatmap(ctx context.Context, osuID int64) error
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

	// Atomic upsert: only touches API-owned fields via $set, defaults
	// local-only fields via $setOnInsert. Returns the full stored document
	// with a valid _id and current local fields.
	user, err := f.users.UpsertOsuFields(ctx, osuID, userOsuFields(resp))
	if err != nil {
		return nil, fmt.Errorf("upsert user from osu! api: %w", err)
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

	// Atomic upsert: only touches API-owned fields via $set.
	bm, err := f.beatmaps.UpsertOsuFields(ctx, osuID, beatmapOsuFields(resp))
	if err != nil {
		return nil, fmt.Errorf("upsert beatmap from osu! api: %w", err)
	}

	f.cacheBeatmap(ctx, bm)
	return bm, nil
}

// --- Cache helpers ---

// InvalidateUser implements Fetcher.
func (f *fetcher) InvalidateUser(ctx context.Context, osuID int64) error {
	return f.rdb.Del(ctx, userCacheKey(osuID)).Err()
}

// InvalidateBeatmap implements Fetcher.
func (f *fetcher) InvalidateBeatmap(ctx context.Context, osuID int64) error {
	return f.rdb.Del(ctx, beatmapCacheKey(osuID)).Err()
}

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

// --- Field mapping helpers ---

// userOsuFields returns a bson.M containing only the fields owned by the
// osu! API. These are the fields that UpsertOsuFields will $set, leaving
// local-only fields (Roles, VerifyStatus, IsBanned) untouched.
func userOsuFields(resp *OsuUserResponse) bson.M {
	return bson.M{
		"username":     resp.Username,
		"avatar_url":   resp.AvatarURL,
		"country_code": resp.Country.Code,
		"global_rank":  resp.Statistics.GlobalRank,
		"pp":           resp.Statistics.PP,
	}
}

// beatmapOsuFields returns a bson.M containing only the fields owned by
// the osu! API. Local-only extended fields are left untouched.
func beatmapOsuFields(resp *OsuBeatmapResponse) bson.M {
	return bson.M{
		"beatmapset_id":     resp.BeatmapsetID,
		"title":             resp.Beatmapset.Title,
		"artist":            resp.Beatmapset.Artist,
		"version":           resp.Version,
		"user_id":           resp.UserID,
		"mode_int":          resp.ModeInt,
		"status":            resp.Status,
		"difficulty_rating": resp.DifficultyRating,
		"bpm":               resp.BPM,
		"total_length":      resp.TotalLength,
		"drain":             resp.Drain,
		"cs":                resp.CS,
		"ar":                resp.AR,
		"accuracy":          resp.Accuracy,
		"cover_url":         resp.Beatmapset.Covers.Cover,
	}
}
