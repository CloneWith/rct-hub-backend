package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// fakeRoomRepo is an in-memory room repository for tests.
type fakeRoomRepo struct {
	rooms map[bson.ObjectID]*domain.Room
	codes map[string]*domain.Room
}

func newFakeRoomRepo() *fakeRoomRepo {
	return &fakeRoomRepo{rooms: make(map[bson.ObjectID]*domain.Room), codes: make(map[string]*domain.Room)}
}

func (r *fakeRoomRepo) Create(ctx context.Context, room *domain.Room) error {
	if _, ok := r.rooms[room.ID]; ok {
		return errs.ErrAlreadyExists
	}
	r.rooms[room.ID] = room
	r.codes[room.Code] = room
	return nil
}

func (r *fakeRoomRepo) Update(ctx context.Context, room *domain.Room) error {
	if _, ok := r.rooms[room.ID]; !ok {
		return errs.ErrNotFound
	}
	r.rooms[room.ID] = room
	r.codes[room.Code] = room
	return nil
}

func (r *fakeRoomRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Room, error) {
	room, ok := r.rooms[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return room, nil
}

func (r *fakeRoomRepo) ByCode(ctx context.Context, code string) (*domain.Room, error) {
	room, ok := r.codes[code]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return room, nil
}

func (r *fakeRoomRepo) List(ctx context.Context, params paginate.Params, roomType *domain.RoomType) (paginate.Result[domain.Room], error) {
	return paginate.Result[domain.Room]{}, nil
}

func (r *fakeRoomRepo) Delete(ctx context.Context, id bson.ObjectID) error {
	room, ok := r.rooms[id]
	if !ok {
		return errs.ErrNotFound
	}
	delete(r.rooms, id)
	delete(r.codes, room.Code)
	return nil
}

// fakeMatchRepo is an in-memory match repository for tests.
type fakeMatchRepo struct {
	matches map[bson.ObjectID]*domain.Match
}

func newFakeMatchRepo() *fakeMatchRepo {
	return &fakeMatchRepo{matches: make(map[bson.ObjectID]*domain.Match)}
}

func (r *fakeMatchRepo) Create(ctx context.Context, match *domain.Match) error {
	r.matches[match.ID] = match
	return nil
}

func (r *fakeMatchRepo) Update(ctx context.Context, match *domain.Match) error {
	if _, ok := r.matches[match.ID]; !ok {
		return errs.ErrNotFound
	}
	r.matches[match.ID] = match
	return nil
}

func (r *fakeMatchRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Match, error) {
	match, ok := r.matches[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return match, nil
}

func (r *fakeMatchRepo) ByCode(ctx context.Context, code string) (*domain.Match, error) {
	return nil, errs.ErrNotFound
}

func (r *fakeMatchRepo) List(ctx context.Context, params paginate.Params, status *domain.MatchStatus) (paginate.Result[domain.Match], error) {
	return paginate.Result[domain.Match]{}, nil
}

// fakeMoveRepo is an in-memory move repository for tests.
type fakeMoveRepo struct {
	moves []domain.Move
}

func newFakeMoveRepo() *fakeMoveRepo {
	return &fakeMoveRepo{}
}

func (r *fakeMoveRepo) Create(ctx context.Context, move *domain.Move) error {
	move.ID = bson.NewObjectID()
	move.CreatedAt = time.Now().UTC()
	r.moves = append(r.moves, *move)
	return nil
}

func (r *fakeMoveRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Move, error) {
	return nil, errs.ErrNotFound
}

func (r *fakeMoveRepo) ByMatch(ctx context.Context, matchID bson.ObjectID, params paginate.Params) (paginate.Result[domain.Move], error) {
	return paginate.Result[domain.Move]{}, nil
}

func (r *fakeMoveRepo) LatestByMatch(ctx context.Context, matchID bson.ObjectID, limit int64) ([]domain.Move, error) {
	return nil, nil
}

type fakeResultRepo struct{}

func newFakeResultRepo() *fakeResultRepo {
	return &fakeResultRepo{}
}

func (r *fakeResultRepo) Create(ctx context.Context, result *domain.Result) error {
	return errs.ErrAlreadyExists
}

func (r *fakeResultRepo) Update(ctx context.Context, result *domain.Result) error {
	return errs.ErrNotFound
}

func (r *fakeResultRepo) ByMatchID(ctx context.Context, matchID bson.ObjectID) (*domain.Result, error) {
	return nil, errs.ErrNotFound
}

func (r *fakeResultRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.Result, error) {
	return nil, errs.ErrNotFound
}

var _ repository.RoomRepository = (*fakeRoomRepo)(nil)
var _ repository.MatchRepository = (*fakeMatchRepo)(nil)
var _ repository.MoveRepository = (*fakeMoveRepo)(nil)
var _ repository.ResultRepository = (*fakeResultRepo)(nil)

func TestRoomServiceCreateAndStartMatch(t *testing.T) {
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	matches := newFakeMatchRepo()
	svc := NewRoomService(rooms, matches)

	room, err := svc.CreateRoom(ctx, 1, domain.RoomTypeCasual, "Test Room")
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if room.Code == "" {
		t.Error("expected room code to be generated")
	}

	// Configure minimum required settings.
	redUID := int64(10)
	blueUID := int64(20)
	if _, err := svc.SetStrategists(ctx, room.ID, &redUID, &blueUID); err != nil {
		t.Fatalf("set strategists: %v", err)
	}
	if _, err := svc.SetBPOrder(ctx, room.ID, domain.BPOrder{FirstPick: domain.TeamSideRed, FirstBan: domain.TeamSideBlue}); err != nil {
		t.Fatalf("set bp order: %v", err)
	}

	match, err := svc.StartMatch(ctx, room.ID)
	if err != nil {
		t.Fatalf("start match: %v", err)
	}
	if match.Status != domain.MatchStatusActive {
		t.Errorf("expected match active, got %s", match.Status)
	}
	if match.TurnState.Phase != domain.MatchPhaseBan {
		t.Errorf("expected ban phase, got %s", match.TurnState.Phase)
	}
	if *match.TurnState.ActiveTeam != domain.TeamSideBlue {
		t.Errorf("expected first ban team blue, got %s", *match.TurnState.ActiveTeam)
	}
}

func TestFormalRoomCannotUseLegacyStartPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rooms := newFakeRoomRepo()
	matches := newFakeMatchRepo()
	room := formalRoomFixture()
	if err := rooms.Create(ctx, &room); err != nil {
		t.Fatal(err)
	}
	svc := NewRoomService(rooms, matches)
	if _, err := svc.StartMatch(ctx, room.ID); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("formal legacy StartMatch error = %v, want conflict", err)
	}
	if len(matches.matches) != 0 || room.MatchID != nil {
		t.Fatalf("formal legacy StartMatch mutated state: matches=%d matchID=%v", len(matches.matches), room.MatchID)
	}
}

func TestMatchServiceBanAndPick(t *testing.T) {
	ctx := context.Background()
	matches := newFakeMatchRepo()
	rooms := newFakeRoomRepo()
	moves := newFakeMoveRepo()
	svc := NewMatchService(matches, rooms, moves, newFakeResultRepo())

	match := makeTestMatch()
	matches.Create(ctx, match)

	admin := domain.RoomMember{UserID: 100, Role: domain.RoomRoleAdmin}
	redStrat := domain.RoomMember{UserID: 10, Role: domain.RoomRoleStrategist, TeamSide: teamSidePtr(domain.TeamSideRed)}

	// Admin can ban at any time.
	if err := svc.BanPiece(ctx, match.ID, admin, domain.PoolSlot{Mod: domain.PieceModNM, Index: 1}); err != nil {
		t.Fatalf("admin ban: %v", err)
	}

	// Red strategist cannot ban during blue's ban turn.
	if err := svc.BanPiece(ctx, match.ID, redStrat, domain.PoolSlot{Mod: domain.PieceModNM, Index: 2}); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}

	// Admin can pick anywhere.
	redSide := domain.TeamSideRed
	if err := svc.PickPiece(ctx, match.ID, admin, domain.PoolSlot{Mod: domain.PieceModNM, Index: 2}, domain.Position{X: 0, Y: 0}, nil, &redSide); err != nil {
		t.Fatalf("admin pick: %v", err)
	}

	// Invalid zone placement for HD.
	if err := svc.PickPiece(ctx, match.ID, admin, domain.PoolSlot{Mod: domain.PieceModHD, Index: 1}, domain.Position{X: 3, Y: 3}, nil, &redSide); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("expected invalid input for wrong zone, got %v", err)
	}
}

func TestFormalMatchCannotUseLegacyWriteMethods(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	matches := newFakeMatchRepo()
	rooms := newFakeRoomRepo()
	moves := newFakeMoveRepo()
	svc := NewMatchService(matches, rooms, moves, newFakeResultRepo())
	match := makeTestMatch()
	match.RoomType = domain.RoomTypeMatch
	if err := matches.Create(ctx, match); err != nil {
		t.Fatal(err)
	}
	admin := domain.RoomMember{UserID: 100, Role: domain.RoomRoleAdmin}
	red := domain.TeamSideRed

	tests := []struct {
		name string
		call func() error
	}{
		{name: "ban", call: func() error {
			return svc.BanPiece(ctx, match.ID, admin, domain.PoolSlot{Mod: domain.PieceModNM, Index: 1})
		}},
		{name: "pick", call: func() error {
			return svc.PickPiece(ctx, match.ID, admin, domain.PoolSlot{Mod: domain.PieceModNM, Index: 1}, domain.Position{}, nil, &red)
		}},
		{name: "rob", call: func() error { return svc.RobPiece(ctx, match.ID, admin, domain.Position{}, domain.Position{X: 1}) }},
		{name: "win", call: func() error { return svc.WinPiece(ctx, match.ID, admin, domain.Position{}, nil) }},
		{name: "end", call: func() error { return svc.EndMatch(ctx, match.ID, domain.WinReasonTB, &red) }},
		{name: "advance", call: func() error { return svc.AdvanceTurn(ctx, match.ID) }},
		{name: "pause", call: func() error { return svc.PauseMatch(ctx, match.ID) }},
		{name: "resume", call: func() error { return svc.ResumeMatch(ctx, match.ID) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, errs.ErrConflict) {
				t.Fatalf("error = %v, want conflict", err)
			}
		})
	}
	if len(moves.moves) != 0 || match.Status != domain.MatchStatusActive {
		t.Fatalf("legacy formal write mutated state: moves=%d status=%s", len(moves.moves), match.Status)
	}
}

func makeTestMatch() *domain.Match {
	match := &domain.Match{
		ID:        bson.NewObjectID(),
		RoomID:    bson.NewObjectID(),
		RoomType:  domain.RoomTypeCasual,
		Mappool:   domain.NewMappool(),
		Board:     domain.NewBoard(),
		BPOrder:   domain.BPOrder{FirstPick: domain.TeamSideRed, FirstBan: domain.TeamSideBlue},
		TurnState: domain.NewTurnState(),
		Timer:     domain.NewTimerState(0, 0),
		Status:    domain.MatchStatusActive,
	}
	match.Mappool.Slots[domain.PieceModNM] = []domain.Piece{{}, {}, {}}
	match.Mappool.Slots[domain.PieceModHD] = []domain.Piece{{}, {}, {}}
	match.TurnState.StartBan(match.BPOrder)
	return match
}

func teamSidePtr(s domain.TeamSide) *domain.TeamSide {
	return &s
}
