package graphql

import (
	"context"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/jwtutil"
	"rctHubBackend/pkg/paginate"
)

// --- 测试替身 ---------------------------------------------------------------

type teamMutationRepo struct {
	created []*domain.Team
	updated []*domain.Team
	deleted []bson.ObjectID
	byID    map[bson.ObjectID]*domain.Team
}

func (r *teamMutationRepo) Create(_ context.Context, t *domain.Team) error {
	if t.ID == bson.NilObjectID {
		t.ID = bson.NewObjectID()
	}
	r.created = append(r.created, t)
	return nil
}
func (r *teamMutationRepo) Update(_ context.Context, t *domain.Team) error {
	r.updated = append(r.updated, t)
	return nil
}
func (r *teamMutationRepo) ByID(_ context.Context, id bson.ObjectID) (*domain.Team, error) {
	if t, ok := r.byID[id]; ok {
		return t, nil
	}
	return nil, errs.ErrNotFound
}
func (r *teamMutationRepo) List(_ context.Context, params paginate.Params, _ string) (paginate.Result[domain.Team], error) {
	params.Normalize()
	items := make([]domain.Team, len(r.created))
	for i := range r.created {
		items[i] = *r.created[i]
	}
	return paginate.NewResult(items, params, int64(len(items))), nil
}
func (r *teamMutationRepo) Delete(_ context.Context, id bson.ObjectID) error {
	r.deleted = append(r.deleted, id)
	return nil
}
func (r *teamMutationRepo) RoomReferenceCount(context.Context, bson.ObjectID) (int64, error) {
	return 0, nil
}

var _ repository.TeamRepository = (*teamMutationRepo)(nil)

type fetcherStub struct {
	user *domain.User
	err  error
}

func (f fetcherStub) GetUser(context.Context, int64) (*domain.User, error) { return f.user, f.err }

// entityAdminResolver 组装一个带 users 读取存根 + 可选 Teams 服务的 resolver。
func entityAdminResolver(user *domain.User, teams *service.TeamService, fetcher UserFetcher) *Resolver {
	services := &service.Services{}
	if teams != nil {
		services.Teams = teams
	}
	resolver := NewResolver(services).WithPrivateReaders(roomQueryUserReader{user: user}, nil)
	if fetcher != nil {
		resolver = resolver.WithUserFetcher(fetcher)
	}
	return resolver
}

func entityAdminViewer(role domain.UserRole) *domain.User {
	return &domain.User{OnlineID: 42, VerifyStatus: domain.Verified, Roles: []domain.UserRole{role}}
}

// --- adminViewer 门控 ---------------------------------------------------------

func TestAdminEntityQueriesRequireAdminViewer(t *testing.T) {
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 42})
	teams := service.NewTeamService(&teamMutationRepo{})

	tests := []struct {
		name    string
		user    *domain.User
		wantErr string
	}{
		{"player is rejected", entityAdminViewer(domain.RolePlayer), "GLOBAL_ROLE_REQUIRED"},
		{"banned admin is rejected", func() *domain.User {
			u := entityAdminViewer(domain.RoleAdmin)
			u.IsBanned = true
			return u
		}(), "ACTION_NOT_ALLOWED"},
		{"admin passes the gate but service is unavailable", entityAdminViewer(domain.RoleAdmin), "team service is unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := entityAdminResolver(test.user, nil, nil)
			if _, err := resolver.Query().Teams(ctx, nil, nil, nil); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("teams error = %v, want %q", err, test.wantErr)
			}
		})
	}

	// 服务齐备时 admin 查询成功，player / 未认证请求被拒。
	admin := entityAdminResolver(entityAdminViewer(domain.RoleAdmin), teams, nil)
	if _, err := admin.Query().Teams(ctx, nil, nil, nil); err != nil {
		t.Fatalf("admin teams query failed: %v", err)
	}
	player := entityAdminResolver(entityAdminViewer(domain.RolePlayer), teams, nil)
	if _, err := player.Query().Teams(ctx, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "GLOBAL_ROLE_REQUIRED") {
		t.Fatalf("player teams error = %v", err)
	}
	if _, err := admin.Query().Teams(context.Background(), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "AUTH_REQUIRED") {
		t.Fatalf("unauthenticated teams error = %v", err)
	}
}

func TestUserByOsuIdRequiresAdminAndFetchesThrough(t *testing.T) {
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 42})
	fetched := &domain.User{OnlineID: 1337, Username: "imported", VerifyStatus: domain.Pending}

	player := entityAdminResolver(entityAdminViewer(domain.RolePlayer), nil, fetcherStub{user: fetched})
	if _, err := player.Query().UserByOsuID(ctx, 1337); err == nil || !strings.Contains(err.Error(), "GLOBAL_ROLE_REQUIRED") {
		t.Fatalf("player userByOsuId error = %v", err)
	}

	admin := entityAdminResolver(entityAdminViewer(domain.RoleAdmin), nil, fetcherStub{user: fetched})
	user, err := admin.Query().UserByOsuID(ctx, 1337)
	if err != nil {
		t.Fatalf("admin userByOsuId failed: %v", err)
	}
	if user.OnlineID != "1337" || user.Username != "imported" {
		t.Fatalf("mapped user = %+v", user)
	}

	noFetcher := entityAdminResolver(entityAdminViewer(domain.RoleAdmin), nil, nil)
	if _, err := noFetcher.Query().UserByOsuID(ctx, 1337); err == nil || !strings.Contains(err.Error(), "user fetcher is unavailable") {
		t.Fatalf("missing fetcher error = %v", err)
	}
	if _, err := admin.Query().UserByOsuID(ctx, 0); err == nil || !strings.Contains(err.Error(), "INVALID_REQUEST") {
		t.Fatalf("non-positive osu id error = %v", err)
	}
}

// --- mutation 全链路 ----------------------------------------------------------

func TestCreateTeamMutationValidatesAndMaps(t *testing.T) {
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 42})
	repo := &teamMutationRepo{}
	resolver := entityAdminResolver(entityAdminViewer(domain.RoleAdmin), service.NewTeamService(repo), nil)

	leader, strategist := 1, 2
	team, err := resolver.Mutation().CreateTeam(ctx, TeamInput{
		Name:         "Alpha",
		LeaderID:     &leader,
		StrategistID: &strategist,
		PlayerIDs:    []int{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("createTeam failed: %v", err)
	}
	if !team.IsReady || team.Name != "Alpha" || len(team.PlayerIDs) != 3 || team.LeaderID == nil || *team.LeaderID != 1 {
		t.Fatalf("mapped team = %+v", team)
	}
	if len(repo.created) != 1 || repo.created[0].Name != "Alpha" {
		t.Fatalf("persisted teams = %+v", repo.created)
	}

	// 队长不在名单内 → 字段级校验错误，且不落库。
	outside := 99
	_, err = resolver.Mutation().CreateTeam(ctx, TeamInput{
		Name:      "Beta",
		LeaderID:  &outside,
		PlayerIDs: []int{1, 2},
	})
	if err == nil || !strings.Contains(err.Error(), "leader_id") {
		t.Fatalf("invalid leader error = %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("invalid team was persisted: %+v", repo.created)
	}
}

func TestDeleteTeamMutationGuards(t *testing.T) {
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 42})
	teamID := bson.NewObjectID()
	repo := &teamMutationRepo{byID: map[bson.ObjectID]*domain.Team{
		teamID: {ID: teamID, Name: "Alpha"},
	}}
	resolver := entityAdminResolver(entityAdminViewer(domain.RoleAdmin), service.NewTeamService(repo), nil)

	ok, err := resolver.Mutation().DeleteTeam(ctx, teamID.Hex())
	if err != nil || !ok {
		t.Fatalf("deleteTeam = %v, %v", ok, err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != teamID {
		t.Fatalf("deleted ids = %v", repo.deleted)
	}
	if _, err := resolver.Mutation().DeleteTeam(ctx, "not-an-objectid"); err == nil {
		t.Fatal("invalid team id accepted")
	}
	player := entityAdminResolver(entityAdminViewer(domain.RolePlayer), service.NewTeamService(repo), nil)
	if _, err := player.Mutation().DeleteTeam(ctx, teamID.Hex()); err == nil || !strings.Contains(err.Error(), "GLOBAL_ROLE_REQUIRED") {
		t.Fatalf("player deleteTeam error = %v", err)
	}
}

// --- 纯映射 -------------------------------------------------------------------

func TestPieceModRoundTrip(t *testing.T) {
	cases := map[PieceMod]domain.PieceMod{
		PieceModNm:    domain.PieceModNM,
		PieceModHd:    domain.PieceModHD,
		PieceModHr:    domain.PieceModHR,
		PieceModDt:    domain.PieceModDT,
		PieceModFm:    domain.PieceModFM,
		PieceModShiro: domain.PieceModShiro, // domain 是混合大小写 "Shiro"
		PieceModTb:    domain.PieceModTB,
	}
	for gqlMod, domainMod := range cases {
		if got := unmapPieceMod(gqlMod); got != domainMod {
			t.Errorf("unmapPieceMod(%s) = %q, want %q", gqlMod, got, domainMod)
		}
		if got := mapPieceMod(domainMod); got != gqlMod {
			t.Errorf("mapPieceMod(%s) = %q, want %q", domainMod, got, gqlMod)
		}
	}
}

func TestMapMappoolReturnsGroupedSortedEntries(t *testing.T) {
	idOf := func(v int64) *int64 { return &v }
	domainPool := &domain.Mappool{
		ID:   bson.NewObjectID(),
		Name: "Pool A",
		Entries: []domain.MappoolEntry{
			{Mod: domain.PieceModTB, Index: 1, BeatmapID: idOf(900)},
			{Mod: domain.PieceModNM, Index: 2, BeatmapID: idOf(101)},
			{Mod: domain.PieceModShiro, Index: 1},
			{Mod: domain.PieceModNM, Index: 1, BeatmapID: idOf(100)},
			{Mod: domain.PieceModHD, Index: 1, BeatmapID: idOf(200)},
		},
	}

	mapped := mapMappool(domainPool)
	if mapped.ID != domainPool.ID.Hex() || mapped.Name != "Pool A" {
		t.Fatalf("mapped mappool = %+v", mapped)
	}
	// 期望：按 mod 规范顺序（NM → HD → … → Shiro → TB）分组，组内按 index 升序。
	wantOrder := []struct {
		mod        PieceMod
		index      int
		hasBeatmap bool
	}{
		{PieceModNm, 1, true},
		{PieceModNm, 2, true},
		{PieceModHd, 1, true},
		{PieceModShiro, 1, false},
		{PieceModTb, 1, true},
	}
	if len(mapped.Entries) != len(wantOrder) {
		t.Fatalf("entries = %+v", mapped.Entries)
	}
	for i, want := range wantOrder {
		got := mapped.Entries[i]
		if got.Mod != want.mod || got.Index != want.index {
			t.Fatalf("entry[%d] = %+v, want mod=%s index=%d", i, got, want.mod, want.index)
		}
		if want.hasBeatmap == (got.BeatmapID == nil) {
			t.Fatalf("entry[%d] beatmap presence mismatch: %+v", i, got)
		}
	}
	if mapped.Entries[0].BeatmapID == nil || *mapped.Entries[0].BeatmapID != 100 {
		t.Fatalf("first NM entry = %+v", mapped.Entries[0])
	}
}
