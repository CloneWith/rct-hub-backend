package graphql

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/jwtutil"
	"rctHubBackend/pkg/paginate"
)

type roomQueryRepo struct {
	rooms      []domain.Room
	teams      []domain.Team
	lifecycles map[bson.ObjectID]string
}

func (r *roomQueryRepo) Create(context.Context, *domain.Room) error                      { return nil }
func (r *roomQueryRepo) Update(context.Context, *domain.Room) error                      { return nil }
func (r *roomQueryRepo) UpdateFields(context.Context, bson.ObjectID, bson.M, bool) error { return nil }
func (r *roomQueryRepo) Delete(context.Context, bson.ObjectID) error                     { return nil }
func (r *roomQueryRepo) ByID(_ context.Context, id bson.ObjectID) (*domain.Room, error) {
	for i := range r.rooms {
		if r.rooms[i].ID == id {
			return &r.rooms[i], nil
		}
	}
	return nil, errs.ErrNotFound
}
func (r *roomQueryRepo) ByCode(_ context.Context, code string) (*domain.Room, error) {
	for i := range r.rooms {
		if r.rooms[i].Code == code {
			return &r.rooms[i], nil
		}
	}
	return nil, errs.ErrNotFound
}
func (r *roomQueryRepo) List(_ context.Context, params paginate.Params, filter repository.RoomListFilter) (paginate.Result[domain.Room], error) {
	params.Normalize()
	items := make([]domain.Room, 0, len(r.rooms))
	for _, room := range r.rooms {
		if filter.Type != nil && room.Type != *filter.Type {
			continue
		}
		if filter.Search != "" && !strings.Contains(strings.ToLower(room.Name), strings.ToLower(filter.Search)) && !strings.Contains(strings.ToLower(room.Code), strings.ToLower(filter.Search)) {
			continue
		}
		if filter.Round != "" && room.Round != filter.Round {
			continue
		}
		if filter.Lifecycle != "" && r.lifecycles[room.ID] != filter.Lifecycle {
			continue
		}
		if filter.RelatedUserID != nil && !roomRelatedTo(room, r.teams, *filter.RelatedUserID) {
			continue
		}
		items = append(items, room)
	}
	start := min(int(params.Skip()), len(items))
	end := min(start+int(params.PerPage), len(items))
	return paginate.NewResult(items[start:end], params, int64(len(items))), nil
}

func roomRelatedTo(room domain.Room, teams []domain.Team, userID int64) bool {
	if room.OwnerID == userID || (room.RefereeUserID != nil && *room.RefereeUserID == userID) {
		return true
	}
	settings := room.Settings
	if settings.StreamerUserID != nil && *settings.StreamerUserID == userID {
		return true
	}
	// Team membership (leader / strategist / player) is resolved through the
	// linked team entities, mirroring the repository's related-user filter.
	for i := range teams {
		team := &teams[i]
		linked := (settings.RedTeamID != nil && team.ID == *settings.RedTeamID) ||
			(settings.BlueTeamID != nil && team.ID == *settings.BlueTeamID)
		if !linked {
			continue
		}
		if (team.LeaderID != nil && *team.LeaderID == userID) ||
			(team.StrategistID != nil && *team.StrategistID == userID) ||
			slices.Contains(team.Players, userID) {
			return true
		}
	}
	return false
}

var _ repository.RoomRepository = (*roomQueryRepo)(nil)

type roomQueryUserReader struct {
	user *domain.User
}

func (r roomQueryUserReader) GetByOsuID(context.Context, int64) (*domain.User, error) {
	if r.user == nil {
		return nil, errs.ErrNotFound
	}
	return r.user, nil
}

func roomQueryResolver(user *domain.User, rooms []domain.Room, teams []domain.Team) *Resolver {
	roomService := service.NewRoomService(&roomQueryRepo{rooms: rooms, teams: teams, lifecycles: make(map[bson.ObjectID]string)}, nil, nil, nil, nil, nil, nil)
	return NewResolver(&service.Services{Rooms: roomService}).WithPrivateReaders(roomQueryUserReader{user: user}, roomService)
}

func TestRoomQueriesRequireVerifiedUnbannedViewer(t *testing.T) {
	roomID := bson.NewObjectID()
	rooms := []domain.Room{{ID: roomID, Code: "ROOM01", Name: "Test room", Type: domain.RoomTypeMatch}}
	queries := []struct {
		name string
		call func(context.Context, QueryResolver) error
	}{
		{name: "rooms", call: func(ctx context.Context, q QueryResolver) error {
			_, err := q.Rooms(ctx, nil, nil, nil, nil, nil, nil, nil)
			return err
		}},
		{name: "room", call: func(ctx context.Context, q QueryResolver) error { _, err := q.Room(ctx, roomID.Hex()); return err }},
		{name: "roomByCode", call: func(ctx context.Context, q QueryResolver) error { _, err := q.RoomByCode(ctx, "ROOM01"); return err }},
	}

	for _, user := range []*domain.User{
		nil,
		{VerifyStatus: domain.Pending},
		{VerifyStatus: domain.Unverified},
		{VerifyStatus: domain.Verified, IsBanned: true},
	} {
		resolver := roomQueryResolver(user, rooms, nil)
		ctx := context.Background()
		if user != nil {
			ctx = WithClaims(ctx, &jwtutil.Claims{OsuID: user.OnlineID})
		}
		for _, query := range queries {
			t.Run(query.name, func(t *testing.T) {
				if err := query.call(ctx, resolver.Query()); err == nil {
					t.Fatalf("expected %s to reject viewer %v", query.name, user)
				}
			})
		}
	}
}

func TestRoomQueriesAllowVerifiedUnbannedViewerAndPreservePagination(t *testing.T) {
	firstID, secondID := bson.NewObjectID(), bson.NewObjectID()
	rooms := []domain.Room{
		{ID: firstID, Code: "ROOM01", Name: "First room", Type: domain.RoomTypeMatch},
		{ID: secondID, Code: "ROOM02", Name: "Second room", Type: domain.RoomTypeCasual},
	}
	user := &domain.User{OnlineID: 42, VerifyStatus: domain.Verified}
	resolver := roomQueryResolver(user, rooms, nil)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: user.OnlineID})

	page, err := resolver.Query().Rooms(ctx, nil, nil, nil, nil, nil, new(2), new(1))
	if err != nil {
		t.Fatalf("rooms: %v", err)
	}
	if page.Total != 2 || page.Page != 2 || page.PerPage != 1 || page.TotalPages != 2 || len(page.Items) != 1 || page.Items[0].ID != secondID.Hex() {
		t.Fatalf("unexpected page: %+v", page)
	}
	room, err := resolver.Query().Room(ctx, firstID.Hex())
	if err != nil || room == nil || room.Code != "ROOM01" {
		t.Fatalf("room: %+v, %v", room, err)
	}
	room, err = resolver.Query().RoomByCode(ctx, "ROOM02")
	if err != nil || room == nil || room.ID != secondID.Hex() {
		t.Fatalf("roomByCode: %+v, %v", room, err)
	}
}

func TestRoomMappingExposesFormalScheduleAndReferee(t *testing.T) {
	refereeID := int64(77)
	scheduledAt := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	room := &domain.Room{ID: bson.NewObjectID(), Type: domain.RoomTypeMatch, OwnerID: 11, RefereeUserID: &refereeID, Round: "semifinal", ScheduledAt: &scheduledAt}
	mapped := mapRoom(room)
	if mapped.RefereeUserID == nil || *mapped.RefereeUserID != "77" || mapped.Round != "semifinal" || mapped.ScheduledAt == nil || !mapped.ScheduledAt.Equal(scheduledAt) {
		t.Fatalf("mapped room = %+v", mapped)
	}
}

func TestRoomsApplySearchRoundStatusAndRelatedFiltersBeforePaging(t *testing.T) {
	user := &domain.User{OnlineID: 42, VerifyStatus: domain.Verified}
	rooms := []domain.Room{
		{ID: bson.NewObjectID(), Code: "NEEDLE-1", Name: "Needle One", Type: domain.RoomTypeMatch, Round: "quarterfinal", OwnerID: 42},
		{ID: bson.NewObjectID(), Code: "NEEDLE-2", Name: "Needle Two", Type: domain.RoomTypeMatch, Round: "quarterfinal", OwnerID: 99},
		{ID: bson.NewObjectID(), Code: "OTHER-1", Name: "Other", Type: domain.RoomTypeMatch, Round: "quarterfinal", OwnerID: 99},
	}
	lifecycles := map[bson.ObjectID]string{
		rooms[0].ID: string(MatchLifecycleSuspended),
		rooms[1].ID: string(MatchLifecycleAdjudicationRequired),
		rooms[2].ID: string(MatchLifecycleRunning),
	}
	repo := &roomQueryRepo{rooms: rooms, lifecycles: lifecycles}
	roomService := service.NewRoomService(repo, nil, nil, nil, nil, nil, nil)
	resolver := NewResolver(&service.Services{Rooms: roomService}).WithPrivateReaders(roomQueryUserReader{user: user}, roomService)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: user.OnlineID})

	pageNumber, perPage := 2, 1
	search, round := "needle", "quarterfinal"
	page, err := resolver.Query().Rooms(ctx, nil, &search, &round, nil, nil, &pageNumber, &perPage)
	if err != nil {
		t.Fatalf("filtered rooms: %v", err)
	}
	if page.Total != 2 || page.TotalPages != 2 || len(page.Items) != 1 || page.Items[0].Code != "NEEDLE-2" {
		t.Fatalf("filtered page = %+v", page)
	}

	for _, expected := range []MatchLifecycle{MatchLifecycleSuspended, MatchLifecycleAdjudicationRequired} {
		status := expected
		filtered, err := resolver.Query().Rooms(ctx, nil, nil, nil, &status, nil, nil, nil)
		if err != nil {
			t.Fatalf("status %s: %v", expected, err)
		}
		if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].Code != "NEEDLE-"+map[MatchLifecycle]string{MatchLifecycleSuspended: "1", MatchLifecycleAdjudicationRequired: "2"}[expected] {
			t.Fatalf("status %s = %+v", expected, filtered)
		}
	}

	strategistTeamID, playerTeamID := bson.NewObjectID(), bson.NewObjectID()
	relatedRooms := []domain.Room{
		{ID: bson.NewObjectID(), Code: "OWNER", OwnerID: 42},
		{ID: bson.NewObjectID(), Code: "REFEREE", RefereeUserID: ptrInt64Value(42)},
		{ID: bson.NewObjectID(), Code: "STRATEGIST", Settings: domain.RoomSettings{RedTeamID: &strategistTeamID}},
		{ID: bson.NewObjectID(), Code: "STREAMER", Settings: domain.RoomSettings{StreamerUserID: ptrInt64Value(42)}},
		{ID: bson.NewObjectID(), Code: "PLAYER", Settings: domain.RoomSettings{BlueTeamID: &playerTeamID}},
		{ID: bson.NewObjectID(), Code: "UNRELATED", OwnerID: 99},
	}
	relatedTeams := []domain.Team{
		{ID: strategistTeamID, Name: "Strategists", StrategistID: ptrInt64Value(42)},
		{ID: playerTeamID, Name: "Players", LeaderID: ptrInt64Value(77), Players: []int64{42}},
	}
	relatedRepo := &roomQueryRepo{rooms: relatedRooms, teams: relatedTeams, lifecycles: make(map[bson.ObjectID]string)}
	relatedService := service.NewRoomService(relatedRepo, nil, nil, nil, nil, nil, nil)
	relatedResolver := NewResolver(&service.Services{Rooms: relatedService}).WithPrivateReaders(roomQueryUserReader{user: user}, relatedService)
	related := true
	relatedPage, err := relatedResolver.Query().Rooms(ctx, nil, nil, nil, nil, &related, nil, nil)
	if err != nil {
		t.Fatalf("related rooms: %v", err)
	}
	if relatedPage.Total != 5 {
		t.Fatalf("related total = %d, want 5", relatedPage.Total)
	}
}

func ptrInt64Value(value int64) *int64 { return new(value) }
