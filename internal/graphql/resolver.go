package graphql

// THIS CODE WILL BE UPDATED WITH SCHEMA CHANGES. PREVIOUS IMPLEMENTATION FOR SCHEMA CHANGES WILL BE KEPT IN THE COMMENT SECTION. IMPLEMENTATION FOR UNCHANGED SCHEMA WILL BE KEPT.

import (
	"context"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
)

type Resolver struct {
	userSvc *service.UserService
}

// NewResolver 创建 GraphQL Resolver，注入所需的 Service 依赖。
// Phase 0 仅需 UserService；Phase 1+ 会扩展为注入 *service.Services。
func NewResolver(userSvc *service.UserService) *Resolver {
	return &Resolver{userSvc: userSvc}
}

// Ping is the resolver for the ping field.
func (r *queryResolver) Ping(context.Context) (string, error) {
	return "pong", nil
}

// Me is the resolver for the "me" field.
func (r *queryResolver) Me(ctx context.Context) (*User, error) {
	claims, ok := ClaimsFromCtx(ctx)
	if !ok || claims == nil {
		return nil, nil // 未认证，返回 null（不报错）
	}

	objID, err := bson.ObjectIDFromHex(claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID in token: %w", err)
	}

	user, err := r.userSvc.Get(ctx, objID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}

	return mapUserToGraphQL(user), nil
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }

// --- 类型映射辅助函数 ---
// Phase 0 手动转换 domain → GraphQL 生成类型
// Phase 1 通过 gqlgen.yml model binding 可消除大部分映射

func mapUserToGraphQL(u *domain.User) *User {
	if u == nil {
		return nil
	}
	return &User{
		ID:           u.ID.Hex(),
		OnlineID:     int(u.OnlineID),
		Username:     u.Username,
		AvatarURL:    u.AvatarURL,
		CountryCode:  u.CountryCode,
		GlobalRank:   new(int(u.GlobalRank)),
		Pp:           new(float64(u.PP)),
		VerifyStatus: VerifyStatus(strings.ToUpper(string(u.VerifyStatus))),
		IsBanned:     u.IsBanned,
		Roles:        mapRolesToGraphQL(u.Roles),
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	}
}

func mapRolesToGraphQL(roles []domain.UserRole) []UserRole {
	result := make([]UserRole, len(roles))
	for i, r := range roles {
		result[i] = UserRole(strings.ToUpper(string(r)))
	}
	return result
}
