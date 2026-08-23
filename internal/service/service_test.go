package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// fakeRoomRepo is an in-memory room repository for tests.
type fakeRoomRepo struct {
	rooms              map[bson.ObjectID]*domain.Room
	codes              map[string]*domain.Room
	beforeUpdateFields func(*domain.Room)
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

func (r *fakeRoomRepo) UpdateFields(_ context.Context, id bson.ObjectID, fields bson.M, requireSetupOpen bool) error {
	room, ok := r.rooms[id]
	if !ok {
		return errs.ErrNotFound
	}
	if r.beforeUpdateFields != nil {
		r.beforeUpdateFields(room)
	}
	if requireSetupOpen && room.MatchID != nil {
		return errs.ErrConflict
	}
	for key, value := range fields {
		switch key {
		case "name":
			room.Name = value.(string)
		case "round":
			room.Round = value.(string)
		case "scheduled_at":
			room.ScheduledAt, _ = value.(*time.Time)
		case "settings.red_strategist_user_id":
			room.Settings.RedStrategistUserID, _ = value.(*int64)
		case "settings.blue_strategist_user_id":
			room.Settings.BlueStrategistUserID, _ = value.(*int64)
		case "settings.streamer_user_id":
			room.Settings.StreamerUserID, _ = value.(*int64)
		case "settings.mappool":
			room.Settings.Mappool = value.(domain.Mappool)
		case "settings.first_pick":
			room.Settings.FirstPick, _ = value.(*domain.TeamSide)
		case "settings.first_ban":
			room.Settings.FirstBan, _ = value.(*domain.TeamSide)
		case "settings.red_leader":
			room.Settings.RedLeader, _ = value.(*int64)
		case "settings.blue_leader":
			room.Settings.BlueLeader, _ = value.(*int64)
		case "settings.red_players":
			room.Settings.RedPlayers = value.([]int64)
		case "settings.blue_players":
			room.Settings.BluePlayers = value.([]int64)
		case "settings.mp_link":
			room.Settings.MPLink, _ = value.(*string)
		case "settings.stream_link":
			room.Settings.StreamLink, _ = value.(*string)
		case "referee_user_id":
			room.RefereeUserID, _ = value.(*int64)
		}
	}
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

func (r *fakeRoomRepo) List(ctx context.Context, params paginate.Params, filter repository.RoomListFilter) (paginate.Result[domain.Room], error) {
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
	for _, match := range r.matches {
		if match.Code == code {
			return match, nil
		}
	}
	return nil, errs.ErrNotFound
}

func (r *fakeMatchRepo) List(ctx context.Context, params paginate.Params, status *domain.MatchStatus) (paginate.Result[domain.Match], error) {
	items := make([]domain.Match, 0, len(r.matches))
	for _, match := range r.matches {
		if status == nil || match.Status == *status {
			items = append(items, *match)
		}
	}
	params.Normalize()
	return paginate.NewResult(items, params, int64(len(items))), nil
}

func (r *fakeMatchRepo) ListFormal(ctx context.Context, params paginate.Params) (paginate.Result[domain.Match], error) {
	items := make([]domain.Match, 0, len(r.matches))
	for _, match := range r.matches {
		if match.RoomType == domain.RoomTypeMatch {
			items = append(items, *match)
		}
	}
	params.Normalize()
	return paginate.NewResult(items, params, int64(len(items))), nil
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
	users := newFakeUserRepo()
	_ = users.Create(ctx, &domain.User{ID: bson.NewObjectID(), OnlineID: 1, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RolePlayer}})
	svc := NewRoomService(rooms, matches, users, nil, nil)

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
	if _, err := svc.SetStrategists(ctx, 1, room.ID, &redUID, &blueUID); err != nil {
		t.Fatalf("set strategists: %v", err)
	}
	if _, err := svc.SetBPOrder(ctx, 1, room.ID, domain.BPOrder{FirstPick: domain.TeamSideRed, FirstBan: domain.TeamSideBlue}); err != nil {
		t.Fatalf("set bp order: %v", err)
	}

	match, err := svc.StartMatch(ctx, 1, room.ID)
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

func TestFormalRoomCreationAssignsItsInitialReferee(t *testing.T) {
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	referee := &domain.User{ID: bson.NewObjectID(), OnlineID: 7, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	if err := users.Create(ctx, referee); err != nil {
		t.Fatal(err)
	}
	svc := NewRoomService(rooms, nil, users, nil, nil)
	room, err := svc.CreateRoom(ctx, referee.OnlineID, domain.RoomTypeMatch, "Formal")
	if err != nil {
		t.Fatalf("create formal room: %v", err)
	}
	if room.RefereeUserID == nil || *room.RefereeUserID != referee.OnlineID {
		t.Fatalf("referee assignment = %v, want %d", room.RefereeUserID, referee.OnlineID)
	}
}

func TestOnlyAdminCanAssignFormalRoomReferee(t *testing.T) {
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 1, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	referee := &domain.User{ID: bson.NewObjectID(), OnlineID: 2, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	otherReferee := &domain.User{ID: bson.NewObjectID(), OnlineID: 3, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	for _, user := range []*domain.User{admin, referee, otherReferee} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	assigned := referee.OnlineID
	room := &domain.Room{ID: bson.NewObjectID(), Type: domain.RoomTypeMatch, OwnerID: referee.OnlineID, RefereeUserID: &assigned}
	if err := rooms.Create(ctx, room); err != nil {
		t.Fatal(err)
	}
	svc := NewRoomService(rooms, nil, users, nil, nil)
	if _, err := svc.SetReferee(ctx, referee.OnlineID, room.ID, &otherReferee.OnlineID); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("referee reassignment error = %v, want forbidden", err)
	}
	if _, err := svc.SetReferee(ctx, admin.OnlineID, room.ID, &otherReferee.OnlineID); err != nil {
		t.Fatalf("admin assignment: %v", err)
	}
	if room.RefereeUserID == nil || *room.RefereeUserID != otherReferee.OnlineID {
		t.Fatalf("stored referee = %v, want %d", room.RefereeUserID, otherReferee.OnlineID)
	}
}

func TestAdminCanUpdateRoomMetadataAndClearOptionalFields(t *testing.T) {
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 1, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	referee := &domain.User{ID: bson.NewObjectID(), OnlineID: 2, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	for _, user := range []*domain.User{admin, referee} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	room := &domain.Room{ID: bson.NewObjectID(), Type: domain.RoomTypeMatch, OwnerID: admin.OnlineID, Name: "old"}
	if err := rooms.Create(ctx, room); err != nil {
		t.Fatal(err)
	}
	svc := NewRoomService(rooms, nil, users, nil, nil)
	scheduled := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	streamer, leader := int64(30), int64(40)
	updated, err := svc.UpdateRoomMetadata(ctx, admin.OnlineID, room.ID, RoomMetadataUpdate{
		Name: "new", ScheduledAt: &scheduled, RefereeUserID: &referee.OnlineID,
		StreamerUserID: &streamer, RedLeader: &leader, RedPlayers: []int64{41, 42}, BluePlayers: []int64{51, 52},
	})
	if err != nil {
		t.Fatalf("update room metadata: %v", err)
	}
	if updated.Name != "new" || updated.ScheduledAt == nil || updated.RefereeUserID == nil || *updated.RefereeUserID != referee.OnlineID || updated.Settings.StreamerUserID == nil {
		t.Fatalf("updated metadata = %+v", updated)
	}
	cleared, err := svc.UpdateRoomMetadata(ctx, admin.OnlineID, room.ID, RoomMetadataUpdate{Name: "cleared"})
	if err != nil {
		t.Fatalf("clear room metadata: %v", err)
	}
	if cleared.ScheduledAt != nil || cleared.RefereeUserID != nil || cleared.Settings.StreamerUserID != nil || len(cleared.Settings.RedPlayers) != 0 {
		t.Fatalf("optional metadata was not cleared: %+v", cleared)
	}
}

func TestOnlyAdminCanUpdateRoomMetadataAndSetupIsLocked(t *testing.T) {
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 1, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	referee := &domain.User{ID: bson.NewObjectID(), OnlineID: 2, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	for _, user := range []*domain.User{admin, referee} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	room := &domain.Room{ID: bson.NewObjectID(), Type: domain.RoomTypeMatch, OwnerID: admin.OnlineID, Name: "old"}
	if err := rooms.Create(ctx, room); err != nil {
		t.Fatal(err)
	}
	svc := NewRoomService(rooms, nil, users, nil, nil)
	if _, err := svc.UpdateRoomMetadata(ctx, referee.OnlineID, room.ID, RoomMetadataUpdate{Name: "nope"}); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("non-admin update error = %v, want forbidden", err)
	}
	matchID := bson.NewObjectID()
	room.MatchID = &matchID
	if _, err := svc.UpdateRoomMetadata(ctx, admin.OnlineID, room.ID, RoomMetadataUpdate{Name: "too late"}); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("post-start update error = %v, want conflict", err)
	}
	if room.Name != "old" {
		t.Fatalf("locked room was changed: %q", room.Name)
	}
}

func TestAdminCanPartiallyUpdateRoomMetadata(t *testing.T) {
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 1, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	referee := &domain.User{ID: bson.NewObjectID(), OnlineID: 2, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	for _, user := range []*domain.User{admin, referee} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	scheduled := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	streamer, leader := int64(30), int64(40)
	room := &domain.Room{ID: bson.NewObjectID(), Type: domain.RoomTypeMatch, OwnerID: admin.OnlineID, Name: "old",
		ScheduledAt: &scheduled, RefereeUserID: &referee.OnlineID,
		Settings: domain.RoomSettings{StreamerUserID: &streamer, RedLeader: &leader, RedPlayers: []int64{41, 42}}}
	if err := rooms.Create(ctx, room); err != nil {
		t.Fatal(err)
	}
	svc := NewRoomService(rooms, nil, users, nil, nil)

	name := "renamed"
	round := "quarterfinal"
	updated, err := svc.UpdateRoomMetadataPartial(ctx, admin.OnlineID, room.ID, RoomMetadataPatch{Name: &name, Round: &round})
	if err != nil {
		t.Fatalf("partial update: %v", err)
	}
	if updated.Name != "renamed" || updated.Round != "quarterfinal" {
		t.Fatalf("patched fields = %+v", updated)
	}
	if updated.ScheduledAt == nil || updated.RefereeUserID == nil || *updated.RefereeUserID != referee.OnlineID ||
		updated.Settings.StreamerUserID == nil || updated.Settings.RedLeader == nil || len(updated.Settings.RedPlayers) != 2 {
		t.Fatalf("untouched fields were modified: %+v", updated)
	}

	if _, err := svc.UpdateRoomMetadataPartial(ctx, referee.OnlineID, room.ID, RoomMetadataPatch{Name: &name}); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("non-admin partial update error = %v, want forbidden", err)
	}
	if _, err := svc.UpdateRoomMetadataPartial(ctx, admin.OnlineID, room.ID, RoomMetadataPatch{}); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("empty patch error = %v, want invalid input", err)
	}

	matchID := bson.NewObjectID()
	room.MatchID = &matchID
	if _, err := svc.UpdateRoomMetadataPartial(ctx, admin.OnlineID, room.ID, RoomMetadataPatch{Name: &name}); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("post-start partial update error = %v, want conflict", err)
	}
}

func TestAdminCannotAssignRefereeToNonFormalRoom(t *testing.T) {
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 1, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	referee := &domain.User{ID: bson.NewObjectID(), OnlineID: 2, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	_ = users.Create(ctx, admin)
	_ = users.Create(ctx, referee)
	room := &domain.Room{ID: bson.NewObjectID(), Type: domain.RoomTypeCasual, OwnerID: admin.OnlineID, Name: "casual"}
	_ = rooms.Create(ctx, room)
	svc := NewRoomService(rooms, nil, users, nil, nil)
	if _, err := svc.UpdateRoomMetadata(ctx, admin.OnlineID, room.ID, RoomMetadataUpdate{Name: "casual", RefereeUserID: &referee.OnlineID}); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("casual referee assignment error = %v, want invalid input", err)
	}
}

func TestFormalRoomUsesAuthoritativeBootstrap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rooms := newFakeRoomRepo()
	matches := newFakeMatchRepo()
	room := formalRoomFixture()
	if err := rooms.Create(ctx, &room); err != nil {
		t.Fatal(err)
	}
	users := newFakeUserRepo()
	_ = users.Create(ctx, &domain.User{ID: bson.NewObjectID(), OnlineID: room.OwnerID, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}})
	bootstrap := &fakeFormalBootstrap{room: rooms.rooms[room.ID]}
	svc := NewRoomService(rooms, matches, users, bootstrap, nil)
	match, err := svc.StartMatch(ctx, room.OwnerID, room.ID)
	if err != nil {
		t.Fatalf("formal StartMatch: %v", err)
	}
	if bootstrap.calls != 1 || match.Status != domain.MatchStatusPending || bootstrap.state.Lifecycle != matchengine.LifecycleReady || bootstrap.state.Version != 0 {
		t.Fatalf("bootstrap calls=%d match=%+v state=%+v", bootstrap.calls, match, bootstrap.state)
	}
	red, blue := int64(102), int64(202)
	if _, err := svc.SetStrategists(ctx, room.OwnerID, room.ID, &red, &blue); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("post-bootstrap assignment edit error = %v, want forbidden", err)
	}
}

func TestFormalStartIsIdempotentAfterBootstrap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rooms := newFakeRoomRepo()
	matches := newFakeMatchRepo()
	room := formalRoomFixture()
	if err := rooms.Create(ctx, &room); err != nil {
		t.Fatal(err)
	}
	users := newFakeUserRepo()
	_ = users.Create(ctx, &domain.User{ID: bson.NewObjectID(), OnlineID: room.OwnerID, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}})
	bootstrap := &fakeFormalBootstrap{room: rooms.rooms[room.ID]}
	svc := NewRoomService(rooms, matches, users, bootstrap, nil)
	first, err := svc.StartMatch(ctx, room.OwnerID, room.ID)
	if err != nil {
		t.Fatalf("first formal StartMatch: %v", err)
	}
	if err := matches.Create(ctx, first); err != nil {
		t.Fatalf("seed legacy match for retry: %v", err)
	}
	second, err := svc.StartMatch(ctx, room.OwnerID, room.ID)
	if err != nil {
		t.Fatalf("repeated formal StartMatch: %v", err)
	}
	if first.ID != second.ID || bootstrap.calls != 1 {
		t.Fatalf("first=%s second=%s bootstrap calls=%d, want same match and one bootstrap", first.ID, second.ID, bootstrap.calls)
	}
}

func TestFormalStartRecoversWhenBootstrapResponseIsLost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rooms := newFakeRoomRepo()
	matches := newFakeMatchRepo()
	room := formalRoomFixture()
	if err := rooms.Create(ctx, &room); err != nil {
		t.Fatal(err)
	}
	existing := &domain.Match{ID: bson.NewObjectID(), RoomID: room.ID, RoomType: domain.RoomTypeMatch, Status: domain.MatchStatusPending}
	if err := matches.Create(ctx, existing); err != nil {
		t.Fatal(err)
	}
	users := newFakeUserRepo()
	_ = users.Create(ctx, &domain.User{ID: bson.NewObjectID(), OnlineID: room.OwnerID, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}})
	bootstrap := &fakeFormalBootstrap{
		room:            rooms.rooms[room.ID],
		createErr:       errs.ErrFormalMatchAlreadyStarted,
		existingMatchID: existing.ID,
	}
	svc := NewRoomService(rooms, matches, users, bootstrap, nil)
	recovered, err := svc.StartMatch(ctx, room.OwnerID, room.ID)
	if err != nil {
		t.Fatalf("formal StartMatch recovery: %v", err)
	}
	if recovered.ID != existing.ID || bootstrap.calls != 1 {
		t.Fatalf("recovered=%s bootstrap calls=%d, want existing match and one attempt", recovered.ID, bootstrap.calls)
	}
}

func TestRoomConfigurationUsesCurrentAccountAndOwnership(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	owner := &domain.User{ID: bson.NewObjectID(), OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RolePlayer}}
	other := &domain.User{ID: bson.NewObjectID(), OnlineID: 200, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RolePlayer}}
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 300, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	for _, user := range []*domain.User{owner, other, admin} {
		_ = users.Create(ctx, user)
	}
	casual := &domain.Room{ID: bson.NewObjectID(), Code: "CASUAL", Type: domain.RoomTypeCasual, OwnerID: owner.OnlineID, Settings: domain.RoomSettings{}}
	formalReferee := owner.OnlineID
	formal := &domain.Room{ID: bson.NewObjectID(), Code: "FORMAL-AUTH", Type: domain.RoomTypeMatch, OwnerID: owner.OnlineID, RefereeUserID: &formalReferee, Settings: domain.RoomSettings{}}
	_ = rooms.Create(ctx, casual)
	_ = rooms.Create(ctx, formal)
	svc := NewRoomService(rooms, newFakeMatchRepo(), users, nil, nil)

	if _, err := svc.SetMPLink(ctx, other.OnlineID, casual.ID, "https://example.test/mp"); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("non-owner error = %v", err)
	}
	if _, err := svc.SetMPLink(ctx, owner.OnlineID, casual.ID, "https://example.test/mp"); err != nil {
		t.Fatalf("casual owner: %v", err)
	}
	if _, err := svc.SetMPLink(ctx, owner.OnlineID, formal.ID, "https://osu.ppy.sh/community/matches/41"); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("formal owner without referee role error = %v", err)
	}
	owner.Roles = []domain.UserRole{domain.RoleReferee}
	if _, err := svc.SetMPLink(ctx, owner.OnlineID, formal.ID, "https://example.test/community/matches/42"); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("invalid formal multiplayer link error = %v", err)
	}
	if _, err := svc.SetMPLink(ctx, owner.OnlineID, formal.ID, "https://osu.ppy.sh/community/matches/42"); err != nil {
		t.Fatalf("assigned referee: %v", err)
	}
	if _, err := svc.SetMPLink(ctx, admin.OnlineID, formal.ID, "https://osu.ppy.sh/community/matches/43"); err != nil {
		t.Fatalf("admin: %v", err)
	}
	owner.IsBanned = true
	if _, err := svc.SetMPLink(ctx, owner.OnlineID, formal.ID, "https://osu.ppy.sh/community/matches/44"); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("banned owner error = %v", err)
	}
}

func TestFormalRefereeCanOnlyUpdateMPLinkAmongRoomSetupEndpoints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 300, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	referee := &domain.User{ID: bson.NewObjectID(), OnlineID: 301, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	_ = users.Create(ctx, admin)
	_ = users.Create(ctx, referee)
	room := formalRoomFixture()
	room.OwnerID = admin.OnlineID
	room.RefereeUserID = &referee.OnlineID
	if err := rooms.Create(ctx, &room); err != nil {
		t.Fatal(err)
	}
	svc := NewRoomService(rooms, newFakeMatchRepo(), users, nil, nil)
	red, blue := int64(10), int64(20)
	if _, err := svc.SetStrategists(ctx, referee.OnlineID, room.ID, &red, &blue); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("referee strategists error = %v, want forbidden", err)
	}
	if _, err := svc.SetStreamer(ctx, referee.OnlineID, room.ID, &red); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("referee streamer error = %v, want forbidden", err)
	}
	if _, err := svc.SetMappool(ctx, referee.OnlineID, room.ID, domain.NewMappool()); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("referee mappool error = %v, want forbidden", err)
	}
	if _, err := svc.SetBPOrder(ctx, referee.OnlineID, room.ID, domain.BPOrder{FirstPick: domain.TeamSideRed, FirstBan: domain.TeamSideBlue}); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("referee bp order error = %v, want forbidden", err)
	}
	if _, err := svc.SetPlayers(ctx, referee.OnlineID, room.ID, &red, &blue, []int64{1}, []int64{2}); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("referee players error = %v, want forbidden", err)
	}
	if _, err := svc.SetStreamLink(ctx, referee.OnlineID, room.ID, "https://stream.example"); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("referee stream link error = %v, want forbidden", err)
	}
	if _, err := svc.SetMPLink(ctx, referee.OnlineID, room.ID, "https://osu.ppy.sh/community/matches/42"); err != nil {
		t.Fatalf("referee mp link: %v", err)
	}
	if _, err := svc.SetStrategists(ctx, admin.OnlineID, room.ID, &red, &blue); err != nil {
		t.Fatalf("admin strategists: %v", err)
	}
}

func TestRoomSetupWriteCannotRaceFormalBootstrap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rooms := newFakeRoomRepo()
	users := newFakeUserRepo()
	admin := &domain.User{ID: bson.NewObjectID(), OnlineID: 999, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	_ = users.Create(ctx, admin)
	room := formalRoomFixture()
	_ = rooms.Create(ctx, &room)
	startedMatchID := bson.NewObjectID()
	rooms.beforeUpdateFields = func(stored *domain.Room) { stored.MatchID = &startedMatchID }
	svc := NewRoomService(rooms, newFakeMatchRepo(), users, nil, nil)
	red, blue := int64(102), int64(202)
	if _, err := svc.SetStrategists(ctx, admin.OnlineID, room.ID, &red, &blue); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("racing setup write error = %v, want conflict", err)
	}
	if room.MatchID == nil || *room.MatchID != startedMatchID || *room.Settings.RedStrategistUserID == red {
		t.Fatalf("racing setup write changed bootstrapped room: %+v", room)
	}
}

type fakeFormalBootstrap struct {
	room            *domain.Room
	state           matchengine.State
	calls           int
	createErr       error
	existingMatchID bson.ObjectID
}

func (f *fakeFormalBootstrap) Create(_ context.Context, _ bson.ObjectID, match domain.Match, state matchengine.State, _ time.Time) error {
	f.calls++
	f.state = state.Clone()
	if f.room != nil {
		matchID := match.ID
		if f.existingMatchID != bson.NilObjectID {
			matchID = f.existingMatchID
		}
		f.room.MatchID = &matchID
	}
	return f.createErr
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
	redStrat := domain.RoomMember{UserID: 10, Role: domain.RoomRoleStrategist, TeamSide: new(domain.TeamSideRed)}

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
