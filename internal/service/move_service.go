package service

import (
	"context"

	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/paginate"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
)

// MoveService handles move history queries.
type MoveService struct {
	moves repository.MoveRepository
}

// NewMoveService creates a new MoveService.
func NewMoveService(moves repository.MoveRepository) *MoveService {
	return &MoveService{moves: moves}
}

// ListByMatch returns paginated moves for a match.
func (s *MoveService) ListByMatch(ctx context.Context, matchID bson.ObjectID, params paginate.Params) (paginate.Result[domain.Move], error) {
	return s.moves.ByMatch(ctx, matchID, params)
}

// LatestByMatch returns the most recent moves for a match.
func (s *MoveService) LatestByMatch(ctx context.Context, matchID bson.ObjectID, limit int64) ([]domain.Move, error) {
	return s.moves.LatestByMatch(ctx, matchID, limit)
}
