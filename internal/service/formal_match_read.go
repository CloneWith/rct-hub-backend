package service

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// FormalMatch combines stable index metadata with the sole authoritative
// MatchEngine state. Legacy match state is deliberately not exposed here.
type FormalMatch struct {
	ID                  bson.ObjectID
	Code                string
	Name                string
	RoomID              bson.ObjectID
	RoomType            domain.RoomType
	Status              domain.MatchStatus
	StrategistReadiness domain.StrategistReadiness
	CreatedAt           time.Time
	Pool                map[string]*int64
	State               matchengine.State
}

// SnapshotReader is the persistence boundary used by formal match queries.
type SnapshotReader interface {
	Load(context.Context, bson.ObjectID) (matchengine.State, error)
	LoadMany(context.Context, []bson.ObjectID) (map[bson.ObjectID]matchengine.State, error)
}

type FormalMatchReadService struct {
	matches   formalMatchRepository
	snapshots SnapshotReader
}

type formalMatchRepository interface {
	ByID(context.Context, bson.ObjectID) (*domain.Match, error)
	ByCode(context.Context, string) (*domain.Match, error)
	ListFormal(context.Context, paginate.Params) (paginate.Result[domain.Match], error)
}

func NewFormalMatchReadService(matches formalMatchRepository, snapshots SnapshotReader) *FormalMatchReadService {
	return &FormalMatchReadService{matches: matches, snapshots: snapshots}
}

func (s *FormalMatchReadService) ByID(ctx context.Context, id bson.ObjectID) (*FormalMatch, error) {
	shell, err := s.matches.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.load(ctx, shell)
}

func (s *FormalMatchReadService) ByCode(ctx context.Context, code string) (*FormalMatch, error) {
	shell, err := s.matches.ByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	return s.load(ctx, shell)
}

func (s *FormalMatchReadService) List(ctx context.Context, params paginate.Params) (paginate.Result[FormalMatch], error) {
	page, err := s.matches.ListFormal(ctx, params)
	if err != nil {
		return paginate.Result[FormalMatch]{}, err
	}
	ids := make([]bson.ObjectID, len(page.Data))
	for index := range page.Data {
		ids[index] = page.Data[index].ID
	}
	states, err := s.snapshots.LoadMany(ctx, ids)
	if err != nil {
		return paginate.Result[FormalMatch]{}, err
	}
	items := make([]FormalMatch, 0, len(page.Data))
	for index := range page.Data {
		shell := &page.Data[index]
		switch shell.RoomType {
		case domain.RoomTypeMatch, domain.RoomTypeCasual, domain.RoomTypePrivate:
		default:
			continue
		}
		state, exists := states[shell.ID]
		if !exists {
			return paginate.Result[FormalMatch]{}, fmt.Errorf("%w: match %s has no authoritative snapshot", errs.ErrConflict, shell.ID.Hex())
		}
		items = append(items, formalMatch(shell, state))
	}
	return paginate.NewResult(items, params, page.Total), nil
}

func (s *FormalMatchReadService) load(ctx context.Context, shell *domain.Match) (*FormalMatch, error) {
	switch shell.RoomType {
	case domain.RoomTypeMatch, domain.RoomTypeCasual, domain.RoomTypePrivate:
	default:
		return nil, errs.ErrNotFound
	}
	state, err := s.snapshots.Load(ctx, shell.ID)
	if err != nil {
		return nil, err
	}
	value := formalMatch(shell, state)
	return &value, nil
}

func formalMatch(shell *domain.Match, state matchengine.State) FormalMatch {
	pool := make(map[string]*int64, len(state.PoolSlots))
	for id := range state.PoolSlots {
		slot, ok := domain.ParsePoolSlot(id)
		if !ok {
			pool[id] = nil
			continue
		}
		piece := shell.Mappool.FindSlot(slot)
		if piece == nil || piece.BeatmapID == nil || *piece.BeatmapID <= 0 {
			pool[id] = nil
			continue
		}
		beatmapID := *piece.BeatmapID
		pool[id] = &beatmapID
	}
	return FormalMatch{
		ID: shell.ID, Code: shell.Code, Name: shell.Name,
		RoomID: shell.RoomID, RoomType: shell.RoomType,
		Status: shell.Status, StrategistReadiness: shell.StrategistReadiness,
		CreatedAt: shell.CreatedAt, Pool: pool, State: state.Clone(),
	}
}
