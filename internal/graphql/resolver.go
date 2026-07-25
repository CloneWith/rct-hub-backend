package graphql

// THIS CODE WILL BE UPDATED WITH SCHEMA CHANGES. PREVIOUS IMPLEMENTATION FOR SCHEMA CHANGES WILL BE KEPT IN THE COMMENT SECTION. IMPLEMENTATION FOR UNCHANGED SCHEMA WILL BE KEPT.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/paginate"
)

type Resolver struct {
	svc *service.Services
}

// NewResolver 创建 GraphQL Resolver，注入全部 Service。
func NewResolver(svc *service.Services) *Resolver {
	return &Resolver{svc: svc}
}

// ============================================================================
// Query Resolver
// ============================================================================

func (r *queryResolver) Ping(context.Context) (string, error) {
	return "pong", nil
}

func (r *queryResolver) Me(ctx context.Context) (*User, error) {
	claims, ok := ClaimsFromCtx(ctx)
	if !ok || claims == nil {
		return nil, nil
	}

	objID, err := bson.ObjectIDFromHex(claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID in token: %w", err)
	}

	user, err := r.svc.Users.Get(ctx, objID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}

	return mapUser(user), nil
}

func (r *queryResolver) Match(ctx context.Context, id string) (*Match, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid match ID: %w", err)
	}

	m, err := r.svc.Matchs.GetMatch(ctx, objID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapMatch(m), nil
}

func (r *queryResolver) MatchByCode(ctx context.Context, code string) (*Match, error) {
	m, err := r.svc.Matchs.GetMatchByCode(ctx, code)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapMatch(m), nil
}

func (r *queryResolver) Matches(ctx context.Context, status *MatchStatus, page *int, perPage *int) (*MatchPage, error) {
	var domainStatus *domain.MatchStatus
	if status != nil {
		s := domain.MatchStatus(strings.ToLower(string(*status)))
		domainStatus = &s
	}

	params := buildPageParams(page, perPage)
	result, err := r.svc.Matchs.List(ctx, params, domainStatus)
	if err != nil {
		return nil, err
	}
	return mapMatchPage(result), nil
}

func (r *queryResolver) Room(ctx context.Context, id string) (*Room, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid room ID: %w", err)
	}

	room, err := r.svc.Rooms.GetRoom(ctx, objID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapRoom(room), nil
}

func (r *queryResolver) RoomByCode(ctx context.Context, code string) (*Room, error) {
	room, err := r.svc.Rooms.GetRoomByCode(ctx, code)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapRoom(room), nil
}

func (r *queryResolver) Rooms(ctx context.Context, typeArg *RoomType, page *int, perPage *int) (*RoomPage, error) {
	var domainType *domain.RoomType
	if typeArg != nil {
		t := domain.RoomType(strings.ToLower(string(*typeArg)))
		domainType = &t
	}

	params := buildPageParams(page, perPage)
	result, err := r.svc.Rooms.GetRooms(ctx, params, domainType)
	if err != nil {
		return nil, err
	}
	return mapRoomPage(result), nil
}

func (r *queryResolver) Beatmap(ctx context.Context, id string) (*Beatmap, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid beatmap ID: %w", err)
	}

	b, err := r.svc.Beatmaps.Get(ctx, objID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapBeatmap(b), nil
}

func (r *queryResolver) BeatmapByOsuID(ctx context.Context, osuID int) (*Beatmap, error) {
	b, err := r.svc.Beatmaps.GetByOsuID(ctx, int64(osuID))
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapBeatmap(b), nil
}

func (r *queryResolver) Beatmaps(ctx context.Context, page *int, perPage *int) (*BeatmapPage, error) {
	params := buildPageParams(page, perPage)
	result, err := r.svc.Beatmaps.List(ctx, params)
	if err != nil {
		return nil, err
	}
	return mapBeatmapPage(result), nil
}

func (r *queryResolver) User(ctx context.Context, id string) (*User, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID: %w", err)
	}

	u, err := r.svc.Users.Get(ctx, objID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapUser(u), nil
}

func (r *queryResolver) Users(ctx context.Context, page *int, perPage *int) (*UserPage, error) {
	params := buildPageParams(page, perPage)
	result, err := r.svc.Users.List(ctx, params)
	if err != nil {
		return nil, err
	}
	return mapUserPage(result), nil
}

func (r *queryResolver) Announcements(ctx context.Context, page *int, perPage *int) (*AnnouncementPage, error) {
	params := buildPageParams(page, perPage)
	result, err := r.svc.Announcements.ListVisible(ctx, params)
	if err != nil {
		return nil, err
	}
	return mapAnnouncementPage(result), nil
}

func (r *queryResolver) Announcement(ctx context.Context, id string) (*Announcement, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid announcement ID: %w", err)
	}

	a, err := r.svc.Announcements.Get(ctx, objID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapAnnouncement(a), nil
}

// ============================================================================
// Match 嵌套字段 Resolver
// ============================================================================

func (r *matchResolver) Moves(ctx context.Context, obj *Match, limit *int, offset *int) ([]*Move, error) {
	matchID, err := bson.ObjectIDFromHex(obj.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid match ID: %w", err)
	}

	// 将 limit/offset 转换为 page/perPage
	params := buildPageParams(limit, limit) // 用 limit 作为 perPage
	if offset != nil && *offset > 0 {
		perPage := params.PerPage
		if perPage <= 0 {
			perPage = 50
		}
		params.Page = int64(*offset)/perPage + 1
		params.PerPage = perPage
	}
	if params.PerPage <= 0 || params.PerPage > 100 {
		params.PerPage = 50
	}

	result, err := r.svc.Moves.ListByMatch(ctx, matchID, params)
	if err != nil {
		return nil, err
	}

	moves := make([]*Move, len(result.Data))
	for i := range result.Data {
		moves[i] = mapMove(&result.Data[i])
	}
	return moves, nil
}

func (r *matchResolver) RecentMove(ctx context.Context, obj *Match) (*Move, error) {
	matchID, err := bson.ObjectIDFromHex(obj.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid match ID: %w", err)
	}

	moves, err := r.svc.Moves.LatestByMatch(ctx, matchID, 1)
	if err != nil {
		return nil, err
	}
	if len(moves) == 0 {
		return nil, nil
	}
	return mapMove(&moves[0]), nil
}

func (r *matchResolver) Room(ctx context.Context, obj *Match) (*Room, error) {
	roomID, err := bson.ObjectIDFromHex(obj.RoomID)
	if err != nil {
		return nil, fmt.Errorf("invalid room ID: %w", err)
	}

	room, err := r.svc.Rooms.GetRoom(ctx, roomID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapRoom(room), nil
}

// ============================================================================
// Match 客户端视图 Resolver (Phase 2 — Read Model §9.10)
// ============================================================================

// StrategistView 策略师视图: @requireRole(role: STRATEGIST) 保证只有策略师可访问。
// 从 GraphQL *Match 类型计算，不重新查库。
func (r *matchResolver) StrategistView(ctx context.Context, obj *Match) (*StrategistView, error) {
	claims, ok := ClaimsFromCtx(ctx)
	if !ok || claims == nil {
		// 正常不应到达此处：@requireRole directive 已拒绝未认证请求
		return nil, fmt.Errorf("AUTH_REQUIRED")
	}
	return computeStrategistView(obj, claims.OsuID), nil
}

// SpectatorView 观众视图: 公开可访问。
// recentMoves 需要调用 MoveService 获取最近 5 条操作记录。
func (r *matchResolver) SpectatorView(ctx context.Context, obj *Match) (*SpectatorView, error) {
	view := computeSpectatorView(obj)

	// 填充 recentMoves (最近 5 条)
	matchID, err := bson.ObjectIDFromHex(obj.ID)
	if err == nil {
		params := paginate.Params{Page: 1, PerPage: 5}
		params.Normalize()
		result, err := r.svc.Moves.ListByMatch(ctx, matchID, params)
		if err == nil && len(result.Data) > 0 {
			view.RecentMoves = make([]*Move, len(result.Data))
			for i := range result.Data {
				view.RecentMoves[i] = mapMove(&result.Data[i])
			}
		}
	}

	return view, nil
}

// OverlayView OBS Overlay 视图: 公开可访问，极简渲染数据。
func (r *matchResolver) OverlayView(ctx context.Context, obj *Match) (*OverlayView, error) {
	return computeOverlayView(obj), nil
}

// RefereeView 裁判视图: @requireRole(role: REFEREE, admin: true) 保证只有裁判/管理员可访问。
func (r *matchResolver) RefereeView(ctx context.Context, obj *Match) (*RefereeView, error) {
	return computeRefereeView(obj), nil
}

// ============================================================================
// Room 嵌套字段 Resolver
// ============================================================================

func (r *roomResolver) Match(ctx context.Context, obj *Room) (*Match, error) {
	if obj.MatchID == nil || *obj.MatchID == "" {
		return nil, nil
	}

	matchID, err := bson.ObjectIDFromHex(*obj.MatchID)
	if err != nil {
		return nil, fmt.Errorf("invalid match ID: %w", err)
	}

	m, err := r.svc.Matchs.GetMatch(ctx, matchID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapMatch(m), nil
}

// ============================================================================
// PoolSlot 嵌套字段 Resolver
// ============================================================================

func (r *poolSlotResolver) Beatmap(ctx context.Context, obj *PoolSlot) (*Beatmap, error) {
	if obj.BeatmapID == nil || *obj.BeatmapID <= 0 {
		return nil, nil // Shiro 或已移除
	}

	// 优先使用请求级 DataLoader（防 N+1）
	loader := BeatmapLoaderFromCtx(ctx)
	if loader != nil {
		b, err := loader.Load(ctx, *obj.BeatmapID)
		if err != nil {
			return nil, err
		}
		return mapBeatmap(b), nil
	}

	// Fallback: 直接调 service（不应发生，handler 应注入 loader）
	b, err := r.svc.Beatmaps.GetByOsuID(ctx, int64(*obj.BeatmapID))
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return mapBeatmap(b), nil
}

// ============================================================================
// Resolver Roots
// ============================================================================

func (r *Resolver) Match() MatchResolver       { return &matchResolver{r} }
func (r *Resolver) PoolSlot() PoolSlotResolver { return &poolSlotResolver{r} }
func (r *Resolver) Query() QueryResolver       { return &queryResolver{r} }
func (r *Resolver) Room() RoomResolver         { return &roomResolver{r} }

type (
	matchResolver    struct{ *Resolver }
	poolSlotResolver struct{ *Resolver }
	queryResolver    struct{ *Resolver }
	roomResolver     struct{ *Resolver }
)

// ============================================================================
// 辅助函数
// ============================================================================

// buildPageParams 将 GraphQL 分页参数 (*int, *int) 转换为 paginate.Params。
// nil 值使用默认值 (page=1, perPage=20)。
func buildPageParams(page, perPage *int) paginate.Params {
	params := paginate.Params{}
	if page != nil {
		params.Page = int64(*page)
	}
	if perPage != nil {
		params.PerPage = int64(*perPage)
	}
	params.Normalize()
	return params
}
