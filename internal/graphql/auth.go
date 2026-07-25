package graphql

import (
	"context"

	"rctHubBackend/pkg/jwtutil"
)

// contextKey 是不可导出的类型，防止与其他包的 context key 冲突。
type contextKey string

const (
	claimsKey contextKey = "gql_claims"
)

// WithClaims 将 JWT claims 注入 context.Context。
// 供 Gin handler wrapper (handler.go) 调用。
func WithClaims(ctx context.Context, claims *jwtutil.Claims) context.Context {
	if claims == nil {
		return ctx
	}
	return context.WithValue(ctx, claimsKey, claims)
}

// ClaimsFromCtx 从 context.Context 中提取 JWT claims。
// 供 GraphQL resolver 调用。如果未认证，返回 (nil, false)。
func ClaimsFromCtx(ctx context.Context) (*jwtutil.Claims, bool) {
	v := ctx.Value(claimsKey)
	if v == nil {
		return nil, false
	}
	claims, ok := v.(*jwtutil.Claims)
	return claims, ok
}
