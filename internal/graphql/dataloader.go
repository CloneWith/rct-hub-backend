package graphql

import (
	"context"
	"sync"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
)

// beatmapLoaderKey 是 context key，用于存储请求级 BeatmapLoader。
type beatmapLoaderKey struct{}

// BeatmapLoader 是请求级缓存加载器，防止 PoolSlot.beatmap 的 N+1 查询。
//
// 工作原理：
//   - 同一个 GraphQL 请求内，多个 PoolSlot 引用同一个 beatmap 时只查一次
//   - 使用 sync.Map 并发安全，适合 gqlgen 并发解析嵌套字段
//   - 不做批量请求（batching），仅做去重缓存
//
// 后续优化：可添加 BeatmapRepository.ByOsuIDs 批量方法，实现真正的 batch loading。
type BeatmapLoader struct {
	svc   *service.BeatmapService
	cache sync.Map // map[int64]*domain.Beatmap
}

// NewBeatmapLoader 创建请求级 BeatmapLoader。
func NewBeatmapLoader(svc *service.BeatmapService) *BeatmapLoader {
	return &BeatmapLoader{svc: svc}
}

// Load 按 osu! beatmap ID 加载谱面，命中缓存则不查库。
// osuID <= 0 (Shiro/removed) 返回 nil, nil。
func (l *BeatmapLoader) Load(ctx context.Context, osuID int64) (*domain.Beatmap, error) {
	if osuID <= 0 {
		return nil, nil
	}

	// 检查缓存
	key := osuID
	if v, ok := l.cache.Load(key); ok {
		return v.(*domain.Beatmap), nil
	}

	// 查库
	b, err := l.svc.GetByOsuID(ctx, key)
	if err != nil {
		return nil, err
	}

	// 写入缓存（StoreOrLoad 防止并发重复查库）
	if b != nil {
		actual, _ := l.cache.LoadOrStore(key, b)
		return actual.(*domain.Beatmap), nil
	}

	// 谱面不存在：缓存 nil 标记防止重复查库
	l.cache.Store(key, (*domain.Beatmap)(nil))
	return nil, nil
}

// WithBeatmapLoader 将 BeatmapLoader 注入 context。
// 供 GinGraphQL handler 在每个请求开始时调用。
func WithBeatmapLoader(ctx context.Context, loader *BeatmapLoader) context.Context {
	if loader == nil {
		return ctx
	}
	return context.WithValue(ctx, beatmapLoaderKey{}, loader)
}

// BeatmapLoaderFromCtx 从 context 中获取 BeatmapLoader。
// 供 poolSlotResolver.Beatmap 调用。
func BeatmapLoaderFromCtx(ctx context.Context) *BeatmapLoader {
	v := ctx.Value(beatmapLoaderKey{})
	if v == nil {
		return nil
	}
	return v.(*BeatmapLoader)
}

// ============================================================================
// UserLoader — 请求级用户缓存加载器
// ============================================================================

type userLoaderKey struct{}

// UserLoader 是请求级缓存加载器，防止嵌套用户字段的 N+1 查询。
type UserLoader struct {
	svc   *service.UserService
	cache sync.Map // map[int64]*domain.User
}

// NewUserLoader 创建请求级 UserLoader。
func NewUserLoader(svc *service.UserService) *UserLoader {
	return &UserLoader{svc: svc}
}

// Load 按 osu! user ID 加载用户，命中缓存则不查库。
// osuID <= 0 返回 nil, nil。
func (l *UserLoader) Load(ctx context.Context, osuID int64) (*domain.User, error) {
	if osuID <= 0 {
		return nil, nil
	}

	key := osuID
	if v, ok := l.cache.Load(key); ok {
		return v.(*domain.User), nil
	}

	u, err := l.svc.GetByOsuID(ctx, key)
	if err != nil {
		return nil, err
	}

	if u != nil {
		actual, _ := l.cache.LoadOrStore(key, u)
		return actual.(*domain.User), nil
	}

	l.cache.Store(key, (*domain.User)(nil))
	return nil, nil
}

// WithUserLoader 将 UserLoader 注入 context。
func WithUserLoader(ctx context.Context, loader *UserLoader) context.Context {
	if loader == nil {
		return ctx
	}
	return context.WithValue(ctx, userLoaderKey{}, loader)
}

// UserLoaderFromCtx 从 context 中获取 UserLoader。
func UserLoaderFromCtx(ctx context.Context) *UserLoader {
	v := ctx.Value(userLoaderKey{})
	if v == nil {
		return nil
	}
	return v.(*UserLoader)
}
