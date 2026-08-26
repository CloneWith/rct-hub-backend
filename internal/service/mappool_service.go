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

// MappoolService handles mappool entity management operations.
type MappoolService struct {
	mappools repository.MappoolRepository
}

func NewMappoolService(mappools repository.MappoolRepository) *MappoolService {
	return &MappoolService{mappools: mappools}
}

// Get returns a mappool by id.
func (s *MappoolService) Get(ctx context.Context, id bson.ObjectID) (*domain.Mappool, error) {
	return s.mappools.ByID(ctx, id)
}

// List returns a paginated mappool directory, optionally filtered by a name
// prefix search.
func (s *MappoolService) List(ctx context.Context, params paginate.Params, search string) (paginate.Result[domain.Mappool], error) {
	return s.mappools.List(ctx, params, search)
}

// MappoolPatch is a partial update request for a mappool. Only non-nil
// fields are applied; entries, when present, replace the whole array
// (wholesale replacement semantics, matching the previous room-level
// mappool PATCH).
type MappoolPatch struct {
	Name        *string                `json:"name,omitempty"`
	Description *string                `json:"description,omitempty"`
	Entries     *[]domain.MappoolEntry `json:"entries,omitempty"`
}

// Create creates a new mappool. Name is required; entries must satisfy the
// slot invariants (unique (mod,index), non-SHIRO entries need a beatmap id,
// SHIRO entries carry none).
func (s *MappoolService) Create(ctx context.Context, m *domain.Mappool) error {
	if m.Name == "" {
		return errs.NewValidationError(errs.FieldError{Field: "name", Rule: "required", Message: "name is required"})
	}
	if m.Entries == nil {
		m.Entries = []domain.MappoolEntry{}
	}
	if problems := m.ValidateEntries(); len(problems) > 0 {
		return entryValidationError(problems)
	}
	normalizeEntryIndexes(m)
	return s.mappools.Create(ctx, m)
}

// Patch applies a partial update to an existing mappool. A present entries
// array replaces the previous one wholesale.
func (s *MappoolService) Patch(ctx context.Context, id bson.ObjectID, patch *MappoolPatch) (*domain.Mappool, error) {
	m, err := s.mappools.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if patch.Name != nil {
		if *patch.Name == "" {
			return nil, errs.NewValidationError(errs.FieldError{Field: "name", Rule: "required", Message: "name is required"})
		}
		m.Name = *patch.Name
	}
	if patch.Description != nil {
		m.Description = patch.Description
	}
	if patch.Entries != nil {
		m.Entries = *patch.Entries
	}
	if problems := m.ValidateEntries(); len(problems) > 0 {
		return nil, entryValidationError(problems)
	}
	if m.Entries == nil {
		m.Entries = []domain.MappoolEntry{}
	}
	normalizeEntryIndexes(m)
	if err := s.mappools.Update(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Delete removes a mappool. Rooms referencing it block the deletion with
// ErrConflict.
func (s *MappoolService) Delete(ctx context.Context, id bson.ObjectID) error {
	if _, err := s.mappools.ByID(ctx, id); err != nil {
		return err
	}
	refs, err := s.mappools.RoomReferenceCount(ctx, id)
	if err != nil {
		return err
	}
	if refs > 0 {
		return &errs.AppError{
			Err:     errs.ErrConflict,
			Message: fmt.Sprintf("mappool is still referenced by %d room(s); detach it from all rooms first", refs),
			Code:    409,
		}
	}
	return s.mappools.Delete(ctx, id)
}

// ToRuntime converts the mappool into the engine-consumable runtime pool.
func (s *MappoolService) ToRuntime(ctx context.Context, id bson.ObjectID) (domain.Pool, error) {
	m, err := s.mappools.ByID(ctx, id)
	if err != nil {
		return domain.Pool{}, err
	}
	return m.ToRuntime(), nil
}

func entryValidationError(problems []string) error {
	fields := make([]errs.FieldError, 0, len(problems))
	for _, p := range problems {
		fields = append(fields, errs.FieldError{
			Field:   p,
			Rule:    "invalid",
			Message: fmt.Sprintf("%s is invalid (check mod, index uniqueness, and beatmap_id requirements)", p),
		})
	}
	return errs.NewValidationError(fields...)
}

// normalizeEntryIndexes rewrites entry indexes to be contiguous 1..N within
// each mod group (in sorted order). Stored indexes then always match the
// runtime pool slot IDs the match engine derives from group positions, even
// when a client submits sparse or unsorted indexes.
func normalizeEntryIndexes(m *domain.Mappool) {
	normalized := m.SortedEntries()
	for _, mod := range []domain.PieceMod{
		domain.PieceModNM, domain.PieceModHD, domain.PieceModHR, domain.PieceModDT,
		domain.PieceModFM, domain.PieceModShiro, domain.PieceModTB,
	} {
		index := 1
		for i := range normalized {
			if normalized[i].Mod == mod {
				normalized[i].Index = index
				index++
			}
		}
	}
	m.Entries = normalized
}
