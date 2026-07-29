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
func (s *MatchService) BanPiece(ctx context.Context, matchID bson.ObjectID, member domain.RoomMember, slot domain.SlotRef) error {
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
func (s *MatchService) PickPiece(ctx context.Context, matchID bson.ObjectID, member domain.RoomMember, slot domain.SlotRef, pos domain.Position, forceMod *domain.ForceMod, placementTeam *domain.TeamSide) error {
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
	if !match.Board.Place(slot.Mod, pos, slot.String(), *placementTeam) {
		return fmt.Errorf("%w: invalid placement", errs.ErrInvalidInput)
	}

	piece.State = domain.PieceStatePicked
	piece.Position = &pos
	teamIDStr := string(*placementTeam)
	piece.TeamID = &teamIDStr
	if slot.Mod == domain.ModFM {
		piece.ForceMod = forceMod
	}

	move := domain.NewPickMove(matchID, match.RoomID, member.UserID, *placementTeam, slot, pos, forceMod)
	if err := s.saveMatchAndMove(ctx, match, move); err != nil {
		return err
	}
	return nil
}

// RobPiece robs an opponent piece, sacrificing one of your own.
func (s *MatchService) RobPiece(ctx context.Context, matchID bson.ObjectID, member domain.RoomMember, from, to domain.Position) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	if !match.CanRob(member) {
		return errs.ErrForbidden
	}

	fromPiece, ok := match.Board.PieceAtPosition(from)
	if !ok || fromPiece.Owner == nil || *fromPiece.Owner == *member.TeamSide {
		return fmt.Errorf("%w: invalid rob source", errs.ErrInvalidInput)
	}
	toPiece, ok := match.Board.PieceAtPosition(to)
	if !ok || toPiece.Owner == nil || *toPiece.Owner != *member.TeamSide {
		return fmt.Errorf("%w: invalid rob sacrifice", errs.ErrInvalidInput)
	}

	teamIDStr := string(*member.TeamSide)

	// Transfer robbed piece to acting team.
	match.Board.TransferOwnership(from, *member.TeamSide)
	if slot, ok := domain.ParseSlotRef(fromPiece.ID); ok {
		if p := match.Mappool.FindSlot(slot); p != nil {
			p.TeamID = &teamIDStr
		}
	}

	// Remove and mark the sacrificed piece as dead.
	match.Board.ClearCell(to)
	if slot, ok := domain.ParseSlotRef(toPiece.ID); ok {
		if p := match.Mappool.FindSlot(slot); p != nil {
			p.State = domain.PieceStateDead
			p.Position = nil
			p.TeamID = nil
		}
	}

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

	piece, ok := match.Board.PieceAtPosition(pos)
	if !ok {
		return fmt.Errorf("%w: no piece at position", errs.ErrInvalidInput)
	}

	winningSide := member.TeamSide
	if winningSide == nil {
		// Admin path: infer winner from existing piece owner.
		if piece.Owner != nil {
			winningSide = piece.Owner
		} else {
			return fmt.Errorf("%w: cannot determine winner", errs.ErrInvalidInput)
		}
	} else if piece.Owner != nil && *piece.Owner != *winningSide {
		return fmt.Errorf("%w: piece does not belong to team", errs.ErrInvalidInput)
	}

	match.Board.SetOwner(pos, *winningSide)

	teamIDStr := string(*winningSide)
	if slot, ok := domain.ParseSlotRef(piece.ID); ok {
		if p := match.Mappool.FindSlot(slot); p != nil {
			p.State = domain.PieceStateWon
			p.TeamID = &teamIDStr
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
func (s *MatchService) EndMatch(ctx context.Context, matchID bson.ObjectID, reason domain.ResultReason, winner *domain.TeamSide) error {
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
	match.TurnState.Phase = domain.PhaseNone
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
	now := time.Now().UTC()
	switch match.TurnState.Action {
	case domain.TurnActionBan:
		match.Timer = domain.Timer{StartedAt: now, Duration: domain.BanDuration}
	case domain.TurnActionPick:
		match.Timer = domain.Timer{StartedAt: now, Duration: domain.PickDuration}
	case domain.TurnActionWin:
		match.Timer = domain.Timer{StartedAt: now, Duration: domain.ResultConfirmationDuration}
	case domain.TurnActionTB:
		match.Timer = domain.Timer{StartedAt: now, Duration: domain.TBPreparationDuration}
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

// ResolveMember determines the RoomMember for a user based on the match's room settings.
func (s *MatchService) ResolveMember(ctx context.Context, matchID bson.ObjectID, userID int64) (domain.RoomMember, error) {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return domain.RoomMember{}, err
	}
	room, err := s.rooms.ByID(ctx, match.RoomID)
	if err != nil {
		return domain.RoomMember{}, err
	}

	member := domain.RoomMember{
		UserID:   userID,
		RoomID:   match.RoomID,
		JoinedAt: time.Now().UTC(),
	}

	switch {
	case room.OwnerID == userID:
		member.Role = domain.RoomRoleAdmin
	case room.Settings.RedStrategistUserID != nil && *room.Settings.RedStrategistUserID == userID:
		member.Role = domain.RoomRoleStrategist
		side := domain.TeamSideRed
		member.TeamSide = &side
	case room.Settings.BlueStrategistUserID != nil && *room.Settings.BlueStrategistUserID == userID:
		member.Role = domain.RoomRoleStrategist
		side := domain.TeamSideBlue
		member.TeamSide = &side
	case room.Settings.StreamerUserID != nil && *room.Settings.StreamerUserID == userID:
		member.Role = domain.RoomRoleStreamer
	default:
		member.Role = domain.RoomRoleSpectator
	}

	return member, nil
}

// PauseMatch pauses the match timer.
func (s *MatchService) PauseMatch(ctx context.Context, matchID bson.ObjectID) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	if match.Timer.Paused {
		return fmt.Errorf("%w: match already paused", errs.ErrInvalidInput)
	}
	match.Timer.Pause(time.Now())
	return s.matches.Update(ctx, match)
}

// ResumeMatch resumes the match timer.
func (s *MatchService) ResumeMatch(ctx context.Context, matchID bson.ObjectID) error {
	match, err := s.matches.ByID(ctx, matchID)
	if err != nil {
		return err
	}
	if !match.Timer.Paused {
		return fmt.Errorf("%w: match not paused", errs.ErrInvalidInput)
	}
	match.Timer.Resume(time.Now())
	return s.matches.Update(ctx, match)
}

func (s *MatchService) saveMatchAndMove(ctx context.Context, match *domain.Match, move domain.Move) error {
	match.UpdatedAt = time.Now().UTC()
	if err := s.matches.Update(ctx, match); err != nil {
		return err
	}
	return s.moves.Create(ctx, &move)
}
