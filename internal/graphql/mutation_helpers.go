package graphql

// Phase 3 — Mutation 辅助函数
// 此文件不包含 resolver 方法，gqlgen 不会覆盖它。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/jwtutil"
)

// requireAuth 从 context 提取 JWT claims，未认证时返回错误。
func requireAuth(ctx context.Context) (*jwtutil.Claims, error) {
	claims, ok := ClaimsFromCtx(ctx)
	if !ok || claims == nil {
		return nil, fmt.Errorf("AUTH_REQUIRED: authentication required")
	}
	return claims, nil
}

// parseMatchID 从字符串解析 bson.ObjectID。
func parseMatchID(id string) (bson.ObjectID, error) {
	return bson.ObjectIDFromHex(id)
}

// mapError 将 service 层 error 转换为 GraphQL MatchError。
func mapError(err error) *MatchError {
	if err == nil {
		return nil
	}

	msg := err.Error()
	if idx := strings.Index(msg, ": "); idx != -1 {
		if errors.Is(err, errs.ErrInvalidInput) || errors.Is(err, errs.ErrForbidden) {
			msg = msg[idx+2:]
		}
	}

	switch {
	case errors.Is(err, errs.ErrForbidden):
		return &MatchError{Code: "ACTION_NOT_ALLOWED", Message: msg}
	case errors.Is(err, errs.ErrInvalidInput):
		return &MatchError{Code: "INVALID_INPUT", Message: msg}
	case errors.Is(err, errs.ErrNotFound):
		return &MatchError{Code: "NOT_FOUND", Message: msg}
	case errors.Is(err, errs.ErrAlreadyExists):
		return &MatchError{Code: "ALREADY_EXISTS", Message: msg}
	case errors.Is(err, errs.ErrUnauthorized):
		return &MatchError{Code: "AUTH_REQUIRED", Message: msg}
	default:
		return &MatchError{Code: "INTERNAL", Message: msg}
	}
}

// notImplemented 返回 NOT_IMPLEMENTED 错误。
func notImplemented(cmd string) *MatchError {
	return &MatchError{
		Code:    "NOT_IMPLEMENTED",
		Message: fmt.Sprintf("%s is not yet implemented", cmd),
	}
}

// emptyEvents 返回空事件数组 (WebSocket 事件系统尚未实现)。
func emptyEvents() []*DomainEventSnapshot {
	return []*DomainEventSnapshot{}
}

// fetchMatchAfterCommand 在命令执行后重新获取 match 并映射为 GraphQL 类型。
func fetchMatchAfterCommand(ctx context.Context, r *mutationResolver, matchID bson.ObjectID) *Match {
	m, err := r.svc.Matchs.GetMatch(ctx, matchID)
	if err != nil {
		return nil
	}
	return mapMatch(m)
}

// poolSlotRefToDomain 将 GraphQL PoolSlotRef 转换为 domain.PoolSlot。
func poolSlotRefToDomain(mod PieceMod, index int) domain.PoolSlot {
	return domain.PoolSlot{
		Mod:   domain.PieceMod(strings.ToUpper(mod.String())),
		Index: index,
	}
}

// positionInputToDomain 将 GraphQL PositionInput 转换为 domain.Position。
func positionInputToDomain(row, col int) domain.Position {
	return domain.Position{X: col, Y: row} // GraphQL col→X, row→Y
}

// teamSideFromGraphQL 将 GraphQL TeamSide 枚举转换为 domain.TeamSide。
func teamSideFromGraphQL(side TeamSide) domain.TeamSide {
	return domain.TeamSide(strings.ToLower(side.String()))
}
