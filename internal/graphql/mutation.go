package graphql

// Phase 3 — Command Mutation Resolver 实现
// 复用现有 MatchService 方法，不引入新业务逻辑分支。

import (
	"context"

	"rctHubBackend/internal/domain"
)

// ============================================================================
// 棋盘操作
// ============================================================================

// BanPoolSlot 禁用池中的一个谱面。
func (r *mutationResolver) BanPoolSlot(ctx context.Context, input BanPoolSlotInput) (*BanPoolSlotResult, error) {
	claims, err := requireAuth(ctx)
	if err != nil {
		return nil, err
	}

	matchID, err := parseMatchID(input.Meta.MatchID)
	if err != nil {
		return &BanPoolSlotResult{
			Success: false,
			Events:  emptyEvents(),
			Error:   &MatchError{Code: "INVALID_INPUT", Message: "invalid match ID"},
		}, nil
	}

	member, err := r.svc.Matchs.ResolveMember(ctx, matchID, claims.OsuID)
	if err != nil {
		return &BanPoolSlotResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	slot := poolSlotRefToDomain(input.Slot.Mod, input.Slot.Index)
	if err := r.svc.Matchs.BanPiece(ctx, matchID, member, slot); err != nil {
		return &BanPoolSlotResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	return &BanPoolSlotResult{
		Success: true,
		Match:   fetchMatchAfterCommand(ctx, r, matchID),
		Events:  emptyEvents(),
	}, nil
}

// UnbanPoolSlot 取消禁用 (尚未实现)。
func (r *mutationResolver) UnbanPoolSlot(ctx context.Context, input UnbanPoolSlotInput) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}
	return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: notImplemented("unbanPoolSlot")}, nil
}

// PlacePiece 从池中选取一个谱面并放置到棋盘上。
func (r *mutationResolver) PlacePiece(ctx context.Context, input PlacePieceInput) (*PlacePieceResult, error) {
	claims, err := requireAuth(ctx)
	if err != nil {
		return nil, err
	}

	matchID, err := parseMatchID(input.Meta.MatchID)
	if err != nil {
		return &PlacePieceResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "invalid match ID"},
		}, nil
	}

	member, err := r.svc.Matchs.ResolveMember(ctx, matchID, claims.OsuID)
	if err != nil {
		return &PlacePieceResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	slot := poolSlotRefToDomain(input.Slot.Mod, input.Slot.Index)
	pos := positionInputToDomain(input.Position.Row, input.Position.Col)

	var forceMod *domain.ForceMod
	if input.ForceMod != nil && *input.ForceMod != "" {
		fm := domain.ForceMod(*input.ForceMod)
		forceMod = &fm
	}

	if err := r.svc.Matchs.PickPiece(ctx, matchID, member, slot, pos, forceMod, nil); err != nil {
		return &PlacePieceResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	return &PlacePieceResult{
		Success: true,
		Match:   fetchMatchAfterCommand(ctx, r, matchID),
		Events:  emptyEvents(),
	}, nil
}

// GrantWinPermission 授予获胜确认权限 (尚未实现)。
func (r *mutationResolver) GrantWinPermission(ctx context.Context, input GrantWinInput) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}
	return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: notImplemented("grantWinPermission")}, nil
}

// ConfirmPieceWinner 确认棋子获胜 (需要 position 参数，当前 schema 未包含)。
func (r *mutationResolver) ConfirmPieceWinner(ctx context.Context, input ConfirmWinnerInput) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}
	return &SimpleCommandResult{
		Success: false, Events: emptyEvents(),
		Error: notImplemented("confirmPieceWinner — requires position specification"),
	}, nil
}

// BeginRobbery 开始抢劫 (尚未实现 — 需要额外的状态追踪)。
func (r *mutationResolver) BeginRobbery(ctx context.Context, input BeginRobberyInput) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}
	return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: notImplemented("beginRobbery")}, nil
}

// CompleteRobbery 完成抢劫操作。从 sacrificePieceIds 和 targetPieceId 推导 from/to 位置。
func (r *mutationResolver) CompleteRobbery(ctx context.Context, input CompleteRobberyInput) (*CompleteRobberyResult, error) {
	claims, err := requireAuth(ctx)
	if err != nil {
		return nil, err
	}

	matchID, err := parseMatchID(input.Meta.MatchID)
	if err != nil {
		return &CompleteRobberyResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "invalid match ID"},
		}, nil
	}

	member, err := r.svc.Matchs.ResolveMember(ctx, matchID, claims.OsuID)
	if err != nil {
		return &CompleteRobberyResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	match, err := r.svc.Matchs.GetMatch(ctx, matchID)
	if err != nil {
		return &CompleteRobberyResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	fromPos, ok := match.Board.FindByPieceID(input.TargetPieceID)
	if !ok {
		return &CompleteRobberyResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "target piece not found on board"},
		}, nil
	}

	if len(input.SacrificePieceIds) == 0 {
		return &CompleteRobberyResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "at least one sacrifice piece required"},
		}, nil
	}
	toPos, ok := match.Board.FindByPieceID(input.SacrificePieceIds[0])
	if !ok {
		return &CompleteRobberyResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "sacrifice piece not found on board"},
		}, nil
	}

	if err := r.svc.Matchs.RobPiece(ctx, matchID, member, fromPos, toPos); err != nil {
		return &CompleteRobberyResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	return &CompleteRobberyResult{
		Success: true,
		Match:   fetchMatchAfterCommand(ctx, r, matchID),
		Events:  emptyEvents(),
	}, nil
}

// CancelRobbery 取消抢劫 (尚未实现)。
func (r *mutationResolver) CancelRobbery(ctx context.Context, input CancelRobberyInput) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}
	return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: notImplemented("cancelRobbery")}, nil
}

// ============================================================================
// 终局
// ============================================================================

// DeclareTbWinner 宣布 Tie-breaker 获胜者。
func (r *mutationResolver) DeclareTbWinner(ctx context.Context, input DeclareTbInput) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}

	matchID, err := parseMatchID(input.Meta.MatchID)
	if err != nil {
		return &SimpleCommandResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "invalid match ID"},
		}, nil
	}

	winner := teamSideFromGraphQL(input.Winner)
	if err := r.svc.Matchs.EndMatch(ctx, matchID, domain.WinReasonTB, &winner); err != nil {
		return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	return &SimpleCommandResult{
		Success: true,
		Match:   fetchMatchAfterCommand(ctx, r, matchID),
		Events:  emptyEvents(),
	}, nil
}

// DeclareSurrender 宣布投降。投降队伍的对手获胜。
func (r *mutationResolver) DeclareSurrender(ctx context.Context, input SurrenderInput) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}

	matchID, err := parseMatchID(input.Meta.MatchID)
	if err != nil {
		return &SimpleCommandResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "invalid match ID"},
		}, nil
	}

	loser := teamSideFromGraphQL(input.Team)
	winner := loser.Opponent()
	if err := r.svc.Matchs.EndMatch(ctx, matchID, domain.WinReasonSurrender, &winner); err != nil {
		return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	return &SimpleCommandResult{
		Success: true,
		Match:   fetchMatchAfterCommand(ctx, r, matchID),
		Events:  emptyEvents(),
	}, nil
}

// ============================================================================
// 回合控制
// ============================================================================

// AdvanceTurn 推进到下一回合。
func (r *mutationResolver) AdvanceTurn(ctx context.Context, input CommandMeta) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}

	matchID, err := parseMatchID(input.MatchID)
	if err != nil {
		return &SimpleCommandResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "invalid match ID"},
		}, nil
	}

	if err := r.svc.Matchs.AdvanceTurn(ctx, matchID); err != nil {
		return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	return &SimpleCommandResult{
		Success: true,
		Match:   fetchMatchAfterCommand(ctx, r, matchID),
		Events:  emptyEvents(),
	}, nil
}

// PauseMatch 暂停比赛计时器。
func (r *mutationResolver) PauseMatch(ctx context.Context, input CommandMeta) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}

	matchID, err := parseMatchID(input.MatchID)
	if err != nil {
		return &SimpleCommandResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "invalid match ID"},
		}, nil
	}

	if err := r.svc.Matchs.PauseMatch(ctx, matchID); err != nil {
		return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	return &SimpleCommandResult{
		Success: true,
		Match:   fetchMatchAfterCommand(ctx, r, matchID),
		Events:  emptyEvents(),
	}, nil
}

// ResumeMatch 恢复比赛计时器。
func (r *mutationResolver) ResumeMatch(ctx context.Context, input CommandMeta) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}

	matchID, err := parseMatchID(input.MatchID)
	if err != nil {
		return &SimpleCommandResult{
			Success: false, Events: emptyEvents(),
			Error: &MatchError{Code: "INVALID_INPUT", Message: "invalid match ID"},
		}, nil
	}

	if err := r.svc.Matchs.ResumeMatch(ctx, matchID); err != nil {
		return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: mapError(err)}, nil
	}

	return &SimpleCommandResult{
		Success: true,
		Match:   fetchMatchAfterCommand(ctx, r, matchID),
		Events:  emptyEvents(),
	}, nil
}

// ============================================================================
// 撤销
// ============================================================================

// UndoAction 撤销操作 (尚未实现)。
func (r *mutationResolver) UndoAction(ctx context.Context, input UndoInput) (*SimpleCommandResult, error) {
	if _, err := requireAuth(ctx); err != nil {
		return nil, err
	}
	return &SimpleCommandResult{Success: false, Events: emptyEvents(), Error: notImplemented("undoAction")}, nil
}
