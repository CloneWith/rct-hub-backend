package service

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// TeamService handles team entity management operations.
type TeamService struct {
	teams repository.TeamRepository
}

func NewTeamService(teams repository.TeamRepository) *TeamService {
	return &TeamService{teams: teams}
}

// Get returns a team by id.
func (s *TeamService) Get(ctx context.Context, id bson.ObjectID) (*domain.Team, error) {
	return s.teams.ByID(ctx, id)
}

// List returns a paginated team directory, optionally filtered by a
// name/seed prefix search.
func (s *TeamService) List(ctx context.Context, params paginate.Params, search string) (paginate.Result[domain.Team], error) {
	return s.teams.List(ctx, params, search)
}

// TeamPatch is a partial update request for a team. Only non-nil fields are
// applied; omitted fields keep their current values.
type TeamPatch struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Seed         *string `json:"seed,omitempty"`
	LeaderID     *int64  `json:"leader_id,omitempty"`
	StrategistID *int64  `json:"strategist_id,omitempty"`
	Players      []int64 `json:"players,omitempty"`
}

// validateTeam applies the shared entity invariants:
//   - name must be non-empty
//   - players must not contain duplicates
//   - when leader/strategist are set they must be listed in players
//
// The caller passes the effective post-patch values.
func validateTeam(name string, leaderID, strategistID *int64, players []int64) []errs.FieldError {
	var fields []errs.FieldError
	if name == "" {
		fields = append(fields, errs.FieldError{Field: "name", Rule: "required", Message: "name is required"})
	}
	seen := make(map[int64]struct{}, len(players))
	for _, id := range players {
		if _, dup := seen[id]; dup {
			fields = append(fields, errs.FieldError{Field: "players", Rule: "unique", Message: fmt.Sprintf("player %d is listed more than once", id)})
			break
		}
		seen[id] = struct{}{}
	}
	contains := func(id int64) bool {
		_, ok := seen[id]
		return ok
	}
	if leaderID != nil && !contains(*leaderID) {
		fields = append(fields, errs.FieldError{Field: "leader_id", Rule: "in_players", Message: "leader_id must be listed in players"})
	}
	if strategistID != nil && !contains(*strategistID) {
		fields = append(fields, errs.FieldError{Field: "strategist_id", Rule: "in_players", Message: "strategist_id must be listed in players"})
	}
	return fields
}

// Create creates a new team. The name is required; description and seed are
// optional. Leader/strategist/players may be set immediately — the same
// invariants as Patch apply.
func (s *TeamService) Create(ctx context.Context, t *domain.Team) error {
	if fields := validateTeam(t.Name, t.LeaderID, t.StrategistID, t.Players); len(fields) > 0 {
		return errs.NewValidationError(fields...)
	}
	if t.Players == nil {
		t.Players = []int64{}
	}
	return s.teams.Create(ctx, t)
}

// Patch applies a partial update to an existing team.
func (s *TeamService) Patch(ctx context.Context, id bson.ObjectID, patch *TeamPatch) (*domain.Team, error) {
	t, err := s.teams.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if patch.Name != nil {
		t.Name = *patch.Name
	}
	if patch.Description != nil {
		t.Description = patch.Description
	}
	if patch.Seed != nil {
		t.Seed = patch.Seed
	}
	if patch.LeaderID != nil {
		t.LeaderID = patch.LeaderID
	}
	if patch.StrategistID != nil {
		t.StrategistID = patch.StrategistID
	}
	if patch.Players != nil {
		t.Players = patch.Players
	}
	if fields := validateTeam(t.Name, t.LeaderID, t.StrategistID, t.Players); len(fields) > 0 {
		return nil, errs.NewValidationError(fields...)
	}
	if t.Players == nil {
		t.Players = []int64{}
	}
	if err := s.teams.Update(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// Delete removes a team. Rooms referencing the team on either side block the
// deletion with ErrConflict so existing room configurations never dangle.
func (s *TeamService) Delete(ctx context.Context, id bson.ObjectID) error {
	if _, err := s.teams.ByID(ctx, id); err != nil {
		return err
	}
	refs, err := s.teams.RoomReferenceCount(ctx, id)
	if err != nil {
		return err
	}
	if refs > 0 {
		return &errs.AppError{
			Err:     errs.ErrConflict,
			Message: fmt.Sprintf("team is still referenced by %d room(s); detach it from all rooms first", refs),
			Code:    409,
		}
	}
	return s.teams.Delete(ctx, id)
}
