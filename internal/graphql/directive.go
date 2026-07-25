package graphql

import (
	"context"
	"fmt"
	"strings"

	"github.com/99designs/gqlgen/graphql"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/jwtutil"
)

// RequireRole implements the @requireRole directive.
//
// 检查 JWT claims 中的角色是否满足要求：
//   - 未认证 → 返回 AUTH_REQUIRED 错误
//   - admin=true 时，ADMIN 角色也可以通过
//   - 否则检查用户是否拥有指定的 role
//
// 通过则调用 next(ctx) 继续执行字段 resolver。
func RequireRole(ctx context.Context, obj any, next graphql.Resolver, role UserRole, admin *bool) (any, error) {
	claims, ok := ClaimsFromCtx(ctx)
	if !ok || claims == nil {
		return nil, fmt.Errorf("AUTH_REQUIRED: authentication required for role %s", role)
	}

	// GraphQL UserRole 值为大写 ("STRATEGIST"), domain.UserRole 为小写 ("strategist")
	requiredRole := domain.UserRole(strings.ToLower(role.String()))

	// admin flag: ADMIN 角色可以绕过角色检查
	adminAllowed := admin != nil && *admin
	if adminAllowed && hasRole(claims, domain.RoleAdmin) {
		return next(ctx)
	}

	// 精确角色匹配
	if !hasRole(claims, requiredRole) {
		return nil, fmt.Errorf("ACTION_NOT_ALLOWED: role %s required, user has %v", role, claims.Roles)
	}

	return next(ctx)
}

// Public implements the @public directive.
//
// 标记字段为公开可访问。这是一个 no-op — 直接调用 next(ctx)。
// 存在的意义是 schema 文档语义：明确标记哪些字段不需要认证。
func Public(ctx context.Context, obj any, next graphql.Resolver) (any, error) {
	return next(ctx)
}

// hasRole 检查 JWT claims 中是否包含指定角色。
func hasRole(claims *jwtutil.Claims, role domain.UserRole) bool {
	if claims == nil || claims.Roles == nil {
		return false
	}
	for _, r := range claims.Roles {
		if r == role {
			return true
		}
	}
	return false
}
