package service

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// MatchService handles match lifecycle and board operations.
type MatchService struct {
	matches repository.MatchRepository
	rooms   repository.RoomRepository
	moves   repository.MoveRepository
}

// NewMatchService creates a new MatchService.
func NewMatchService(matches repository.MatchRepository, rooms repository.RoomRepository, moves repository.MoveRepository) *MatchService {
	return &MatchService{matches: matches, rooms: rooms, moves: moves}
}

// List returns a paginated list of matches filtered by optional status.
func (s *MatchService) List(ctx context.Context, params paginate.Params, status *domain.MatchStatus) (paginate.Result[domain.Match], error) {
	return s.matches.List(ctx, params, status)
}

// GetMatch fetches a match by id.
func (s *MatchService) GetMatch(ctx context.Context, id bson.ObjectID) (*domain.Match, error) {
	return s.matches.ByID(ctx, id)
}

// GetMatchByCode fetches a match by code.
func (s *MatchService) GetMatchByCode(ctx context.Context, code string) (*domain.Match, error) {
	return s.matches.ByCode(ctx, code)
}

// BanPiece bans a piece from the mappool.
func (s *MatchService) BanPiece(ctx context.Context, matchID bson.ObjectID, member domain.RoomMember, slot domain.PoolSlot) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	if !match.CanBan(member) {
		return errs.ErrForbidden
	}
	piece := match.Mappool.FindSlot(slot)
	if piece == nil {
		return fmt.Errorf("%w: piece not found", errs.ErrInvalidInput)
	}
	if !piece.CanBeSelected() {
		return fmt.Errorf("%w: piece cannot be banned", errs.ErrInvalidInput)
	}

	piece.State = domain.PieceStateBanned
	move := domain.Move{
		MatchID:    matchID,
		RoomID:     match.RoomID,
		Type:       domain.MoveTypeBan,
		TeamSide:   member.TeamSide,
		OperatorID: member.UserID,
		Slot:       &slot,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.saveMatchAndMove(ctx, match, move); err != nil {
		return err
	}
	return nil
}

// PickPiece picks a piece and places it on the board for the given placement team.
func (s *MatchService) PickPiece(ctx context.Context, matchID bson.ObjectID, member domain.RoomMember, slot domain.PoolSlot, pos domain.Position, forceMod *domain.ForceMod, placementTeam *domain.TeamSide) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	if !match.CanPick(member) {
		return errs.ErrForbidden
	}
	if placementTeam == nil {
		placementTeam = member.TeamSide
	}
	if placementTeam == nil {
		return fmt.Errorf("%w: placement team required", errs.ErrInvalidInput)
	}
	if member.Role != domain.RoomRoleAdmin && member.TeamSide != nil && *member.TeamSide != *placementTeam {
		return errs.ErrForbidden
	}
	piece := match.Mappool.FindSlot(slot)
	if piece == nil {
		return fmt.Errorf("%w: piece not found", errs.ErrInvalidInput)
	}
	if !piece.CanBeSelected() {
		return fmt.Errorf("%w: piece cannot be picked", errs.ErrInvalidInput)
	}
	if !match.Board.Place(slot.Mod, pos, slot.String(), string(*placementTeam)) {
		return fmt.Errorf("%w: invalid placement", errs.ErrInvalidInput)
	}

	piece.State = domain.PieceStatePicked
	piece.Position = &pos
	piece.TeamID = stringPtr(string(*placementTeam))
	if slot.Mod == domain.PieceModFM {
		piece.ForceMod = forceMod
	}

	move := domain.NewPickMove(matchID, match.RoomID, member.UserID, *placementTeam, slot, pos, forceMod)
	if err := s.saveMatchAndMove(ctx, match, move); err != nil {
		return err
	}
	return nil
}

// RobPiece robs an opponent piece, sacrificing one of your own.
// from is the opponent piece being robbed; to is the acting team's piece being sacrificed.
func (s *MatchService) RobPiece(ctx context.Context, matchID bson.ObjectID, member domain.RoomMember, from, to domain.Position) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	if !match.CanRob(member) {
		return errs.ErrForbidden
	}
	fromCell := match.Board.CellAt(from)
	if fromCell == nil || fromCell.State != domain.CellStateOccupied || fromCell.PieceID == nil || fromCell.TeamID == nil || *fromCell.TeamID == string(*member.TeamSide) {
		return fmt.Errorf("%w: invalid rob source", errs.ErrInvalidInput)
	}
	toCell := match.Board.CellAt(to)
	if toCell == nil || toCell.State != domain.CellStateOccupied || toCell.PieceID == nil || toCell.TeamID == nil || *toCell.TeamID != string(*member.TeamSide) {
		return fmt.Errorf("%w: invalid rob sacrifice", errs.ErrInvalidInput)
	}

	teamID := string(*member.TeamSide)

	// Transfer robbed piece to acting team.
	fromCell.TeamID = &teamID
	if slot, ok := domain.ParsePoolSlot(*fromCell.PieceID); ok {
		if p := match.Mappool.FindSlot(slot); p != nil {
			p.TeamID = &teamID
		}
	}

	// Remove and mark the sacrificed piece as dead.
	if slot, ok := domain.ParsePoolSlot(*toCell.PieceID); ok {
		if p := match.Mappool.FindSlot(slot); p != nil {
			p.State = domain.PieceStateDead
			p.Position = nil
			p.TeamID = nil
		}
	}
	toCell.State = domain.CellStateEmpty
	toCell.PieceID = nil
	toCell.TeamID = nil

	move := domain.NewRobMove(matchID, match.RoomID, member.UserID, *member.TeamSide, from, to)
	if err := s.saveMatchAndMove(ctx, match, move); err != nil {
		return err
	}
	return nil
}

// WinPiece marks a placed piece as won.
func (s *MatchService) WinPiece(ctx context.Context, matchID bson.ObjectID, member domain.RoomMember, pos domain.Position, winEnabledForTeam map[domain.TeamSide]bool) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	if !match.CanWin(member, winEnabledForTeam) {
		return errs.ErrForbidden
	}
	cell := match.Board.CellAt(pos)
	if cell == nil || cell.State != domain.CellStateOccupied || cell.PieceID == nil {
		return fmt.Errorf("%w: no piece at position", errs.ErrInvalidInput)
	}

	winningSide := member.TeamSide
	if winningSide == nil {
		// Admin path: infer winner from the existing cell owner.
		if cell.TeamID != nil {
			side := domain.TeamSide(*cell.TeamID)
			winningSide = &side
		} else {
			return fmt.Errorf("%w: cannot determine winner", errs.ErrInvalidInput)
		}
	} else if cell.TeamID != nil && *cell.TeamID != string(*winningSide) {
		// Strategist may only win pieces already owned by their team.
		return fmt.Errorf("%w: piece does not belong to team", errs.ErrInvalidInput)
	}

	teamID := string(*winningSide)
	cell.TeamID = &teamID
	if slot, ok := domain.ParsePoolSlot(*cell.PieceID); ok {
		if p := match.Mappool.FindSlot(slot); p != nil {
			p.State = domain.PieceStateWon
			p.TeamID = &teamID
		}
	}

	move := domain.Move{
		MatchID:    matchID,
		RoomID:     match.RoomID,
		Type:       domain.MoveTypeWin,
		TeamSide:   winningSide,
		OperatorID: member.UserID,
		To:         &pos,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.saveMatchAndMove(ctx, match, move); err != nil {
		return err
	}
	return nil
}

// EndMatch ends the match with the given reason and optional winner.
func (s *MatchService) EndMatch(ctx context.Context, matchID bson.ObjectID, reason domain.WinReason, winner *domain.TeamSide) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	if match.IsFinished() {
		return errs.ErrAlreadyExists
	}
	match.Status = domain.MatchStatusFinished
	now := time.Now().UTC()
	match.FinishedAt = &now
	match.TurnState.Phase = domain.MatchPhaseEnded
	if err := s.matches.Update(ctx, match); err != nil {
		return err
	}
	return nil
}

// AdvanceTurn moves the match to the next turn.
func (s *MatchService) AdvanceTurn(ctx context.Context, matchID bson.ObjectID) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	match.TurnState.Next(match.BPOrder)
	switch match.TurnState.Action {
	case domain.TurnActionBan:
		match.Timer = domain.NewTimerState(domain.BanTimeLimit, domain.BanBonusTime)
	case domain.TurnActionPick:
		match.Timer = domain.NewTimerState(domain.PickTimeLimit, domain.PickBonusTime)
	case domain.TurnActionWin:
		match.Timer = domain.NewTimerState(domain.WinTimeLimit, domain.WinBonusTime)
	case domain.TurnActionTB:
		match.Timer = domain.NewTimerState(domain.TBTimeLimit, 0)
	}
	return s.matches.Update(ctx, match)
}

// CheckWinCondition checks for four-in-a-row and returns the winning side if any.
func (s *MatchService) CheckWinCondition(ctx context.Context, matchID bson.ObjectID) (*domain.TeamSide, error) {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return nil, err
	}
	return match.WinningTeamID(), nil
}

// ListByMatch returns paginated moves for a match.
func (s *MatchService) ListByMatch(ctx context.Context, matchID bson.ObjectID, params paginate.Params) (paginate.Result[domain.Move], error) {
	return s.moves.ByMatch(ctx, matchID, params)
}

// LatestByMatch returns the most recent moves for a match.
func (s *MatchService) LatestByMatch(ctx context.Context, matchID bson.ObjectID, limit int64) ([]domain.Move, error) {
	return s.moves.LatestByMatch(ctx, matchID, limit)
}

func (s *MatchService) saveMatchAndMove(ctx context.Context, match *domain.Match, move domain.Move) error {
	match.UpdatedAt = time.Now().UTC()
	if err := s.matches.Update(ctx, match); err != nil {
		return err
	}
	return s.moves.Create(ctx, &move)
}

func stringPtr(s string) *string {
	return &s
}
