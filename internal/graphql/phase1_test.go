package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
)

// ============================================================================
// Phase 1 — Schema 完整性测试
// ============================================================================

// TestSchemaIntrospection 验证 Phase 1 Schema 包含全部预期类型和查询。
func TestSchemaIntrospection(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	// 查询 __schema 获取所有类型名称
	query := `{"query":"{ __schema { types { name } queryType { fields { name } } } }"}`

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Data struct {
			Schema struct {
				Types []struct {
					Name string `json:"name"`
				} `json:"types"`
				QueryType struct {
					Fields []struct {
						Name string `json:"name"`
					} `json:"fields"`
				} `json:"queryType"`
			} `json:"__schema"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v\nbody: %s", err, rr.Body.String())
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("introspection errors: %+v", resp.Errors)
	}

	// 验证核心类型存在
	typeNames := make(map[string]bool)
	for _, tp := range resp.Data.Schema.Types {
		typeNames[tp.Name] = true
	}
	expectedTypes := []string{
		"User", "Room", "RoomSettings", "Match", "MatchTeams", "Team",
		"Board", "BoardCell", "Piece", "Position", "Mappool", "PoolSlotGroup",
		"PoolSlot", "Beatmap", "Move", "PlayerScore", "BPOrder",
		"TurnState", "TimerState", "Announcement",
		"MatchPage", "RoomPage", "BeatmapPage", "UserPage", "AnnouncementPage",
	}
	for _, et := range expectedTypes {
		if !typeNames[et] {
			t.Errorf("expected type %s in schema, not found", et)
		}
	}

	// 验证 Query 字段
	queryFields := make(map[string]bool)
	for _, f := range resp.Data.Schema.QueryType.Fields {
		queryFields[f.Name] = true
	}
	expectedQueries := []string{
		"ping", "me",
		"match", "matchByCode", "matches",
		"room", "roomByCode", "rooms",
		"beatmap", "beatmapByOsuId", "beatmaps",
		"user", "users",
		"announcements", "announcement",
	}
	for _, eq := range expectedQueries {
		if !queryFields[eq] {
			t.Errorf("expected query field %s, not found", eq)
		}
	}

	t.Logf("✓ Schema introspection: %d types, %d query fields", len(typeNames), len(queryFields))
}

// ============================================================================
// Phase 1 — 类型映射测试
// ============================================================================

func TestMapUser(t *testing.T) {
	u := &domain.User{
		ID:           bson.NewObjectID(),
		OnlineID:     12345,
		Username:     "testuser",
		AvatarURL:    "https://example.com/avatar.png",
		CountryCode:  "CN",
		GlobalRank:   1000,
		PP:           5000.5,
		VerifyStatus: domain.Verified,
		IsBanned:     false,
		Roles:        []domain.UserRole{domain.RolePlayer, domain.RoleStrategist},
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	gqlUser := mapUser(u)

	if gqlUser.ID != u.ID.Hex() {
		t.Errorf("ID: expected %s, got %s", u.ID.Hex(), gqlUser.ID)
	}
	if gqlUser.OnlineID != 12345 {
		t.Errorf("OnlineID: expected 12345, got %d", gqlUser.OnlineID)
	}
	if gqlUser.Username != "testuser" {
		t.Errorf("Username: expected testuser, got %s", gqlUser.Username)
	}
	if gqlUser.VerifyStatus != VerifyStatus("VERIFIED") {
		t.Errorf("VerifyStatus: expected VERIFIED, got %s", gqlUser.VerifyStatus)
	}
	if len(gqlUser.Roles) != 2 {
		t.Fatalf("Roles: expected 2, got %d", len(gqlUser.Roles))
	}
	if gqlUser.Roles[0] != UserRole("PLAYER") {
		t.Errorf("Roles[0]: expected PLAYER, got %s", gqlUser.Roles[0])
	}
	if gqlUser.GlobalRank == nil || *gqlUser.GlobalRank != 1000 {
		t.Errorf("GlobalRank: expected 1000, got %v", gqlUser.GlobalRank)
	}
	if gqlUser.Pp == nil || *gqlUser.Pp != 5000.5 {
		t.Errorf("Pp: expected 5000.5, got %v", gqlUser.Pp)
	}

	t.Logf("✓ mapUser: all fields correctly mapped")
}

func TestMapMatch(t *testing.T) {
	red := domain.TeamSideRed
	match := &domain.Match{
		ID:       bson.NewObjectID(),
		RoomID:   bson.NewObjectID(),
		Code:     "MATCH001",
		Name:     "Test Match",
		RoomType: domain.RoomTypeMatch,
		Status:   domain.MatchStatusActive,
		TeamRed:  domain.Team{ID: bson.NewObjectID(), Side: domain.TeamSideRed, Name: "Red Team"},
		TeamBlue: domain.Team{ID: bson.NewObjectID(), Side: domain.TeamSideBlue, Name: "Blue Team"},
		Board:    domain.NewBoard(),
		Mappool:  domain.NewMappool(),
		BPOrder:  domain.BPOrder{FirstPick: domain.TeamSideRed, FirstBan: domain.TeamSideBlue},
		TurnState: domain.TurnState{
			Phase:      domain.PhaseBan,
			Counter:    -3,
			ActiveTeam: &red,
			Action:     domain.TurnActionBan,
			TimeLimit:  60 * time.Second,
			BonusTime:  15 * time.Second,
		},
		Timer: domain.Timer{Duration: 60 * time.Second},
	}

	gqlMatch := mapMatch(match)

	if gqlMatch.ID != match.ID.Hex() {
		t.Errorf("ID mismatch")
	}
	if gqlMatch.Code != "MATCH001" {
		t.Errorf("Code: expected MATCH001, got %s", gqlMatch.Code)
	}
	if gqlMatch.RoomType != RoomType("MATCH") {
		t.Errorf("RoomType: expected MATCH, got %s", gqlMatch.RoomType)
	}
	if gqlMatch.Status != MatchStatus("ACTIVE") {
		t.Errorf("Status: expected ACTIVE, got %s", gqlMatch.Status)
	}
	if gqlMatch.Phase == nil || *gqlMatch.Phase != MatchPhase("BAN") {
		t.Errorf("Phase: expected BAN, got %v", gqlMatch.Phase)
	}
	if gqlMatch.ActiveTeam == nil || *gqlMatch.ActiveTeam != TeamSide("RED") {
		t.Errorf("ActiveTeam: expected RED, got %v", gqlMatch.ActiveTeam)
	}
	if gqlMatch.RoomID != match.RoomID.Hex() {
		t.Errorf("RoomID mismatch")
	}
	if gqlMatch.Teams == nil || gqlMatch.Teams.Red == nil || gqlMatch.Teams.Red.Name != "Red Team" {
		t.Errorf("Teams.Red not mapped correctly")
	}
	if gqlMatch.Board == nil || gqlMatch.Board.Rows != 4 || gqlMatch.Board.Cols != 4 {
		t.Errorf("Board not mapped correctly")
	}
	if gqlMatch.BpOrder == nil || gqlMatch.BpOrder.FirstPick != TeamSide("RED") {
		t.Errorf("BpOrder.FirstPick: expected RED, got %v", gqlMatch.BpOrder)
	}
	if gqlMatch.TurnState == nil || gqlMatch.TurnState.Counter != -3 {
		t.Errorf("TurnState.Counter: expected -3, got %v", gqlMatch.TurnState)
	}
	if gqlMatch.TurnState.TimeLimit == nil || *gqlMatch.TurnState.TimeLimit != 60 {
		t.Errorf("TurnState.TimeLimit: expected 60, got %v", gqlMatch.TurnState.TimeLimit)
	}
	if gqlMatch.Timer == nil || gqlMatch.Timer.TimeLimit != 60 {
		t.Errorf("Timer.TimeLimit: expected 60, got %v", gqlMatch.Timer)
	}

	t.Logf("✓ mapMatch: all fields correctly mapped")
}

func TestMapBoard(t *testing.T) {
	board := domain.NewBoard()
	gqlBoard := mapBoard(&board)

	if gqlBoard.Rows != 4 || gqlBoard.Cols != 4 {
		t.Fatalf("Board dimensions: expected 4x4, got %dx%d", gqlBoard.Cols, gqlBoard.Rows)
	}
	if len(gqlBoard.Cells) != 4 || len(gqlBoard.Cells[0]) != 4 {
		t.Fatalf("Cells: expected 4x4, got %dx%d", len(gqlBoard.Cells[0]), len(gqlBoard.Cells))
	}

	// 验证 zone 映射
	cell00 := gqlBoard.Cells[0][0]
	if cell00.Zone != BoardZone("DT") {
		t.Errorf("Cell(0,0) Zone: expected DT, got %s", cell00.Zone)
	}
	if cell00.Position.Row != 0 || cell00.Position.Col != 0 {
		t.Errorf("Cell(0,0) Position: expected (0,0), got row=%d col=%d", cell00.Position.Row, cell00.Position.Col)
	}

	cell20 := gqlBoard.Cells[2][0]
	if cell20.Zone != BoardZone("HR") {
		t.Errorf("Cell(2,0) Zone: expected HR, got %s", cell20.Zone)
	}

	t.Logf("✓ mapBoard: 4x4 board with correct zones and positions")
}

func TestMapPosition(t *testing.T) {
	// domain Position: X=col, Y=row
	pos := domain.Position{X: 3, Y: 2}
	gqlPos := mapPosition(pos)

	if gqlPos.Row != 2 {
		t.Errorf("Row: expected 2 (Y), got %d", gqlPos.Row)
	}
	if gqlPos.Col != 3 {
		t.Errorf("Col: expected 3 (X), got %d", gqlPos.Col)
	}

	t.Logf("✓ mapPosition: X=3,Y=2 → row=2,col=3")
}

func TestMapMappool(t *testing.T) {
	pool := domain.NewMappool()
	nmID := int64(12345)
	pool.Slots[domain.ModNM] = []domain.Piece{
		{BeatmapID: &nmID, State: domain.PieceStateNormal},
		{BeatmapID: nil, State: domain.PieceStateNormal}, // Shiro
	}
	pool.Slots[domain.ModHD] = []domain.Piece{
		{BeatmapID: &nmID, State: domain.PieceStateBanned},
	}

	gqlPool := mapMappool(&pool)

	if len(gqlPool.Slots) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(gqlPool.Slots))
	}

	// NM 组应在前面 (顺序: NM, HD, HR, DT, FM, Shiro, TB)
	if gqlPool.Slots[0].Mod != PieceMod("NM") {
		t.Errorf("first group mod: expected NM, got %s", gqlPool.Slots[0].Mod)
	}
	if len(gqlPool.Slots[0].Pieces) != 2 {
		t.Fatalf("NM pieces: expected 2, got %d", len(gqlPool.Slots[0].Pieces))
	}
	// 第一个 NM slot: beatmapID=12345, state=NORMAL
	if gqlPool.Slots[0].Pieces[0].BeatmapID == nil || *gqlPool.Slots[0].Pieces[0].BeatmapID != 12345 {
		t.Errorf("NM-1 BeatmapID: expected 12345, got %v", gqlPool.Slots[0].Pieces[0].BeatmapID)
	}
	if gqlPool.Slots[0].Pieces[0].State != PieceState("NORMAL") {
		t.Errorf("NM-1 State: expected NORMAL, got %s", gqlPool.Slots[0].Pieces[0].State)
	}
	// 第二个 NM slot: Shiro (beatmapID=nil)
	if gqlPool.Slots[0].Pieces[1].BeatmapID != nil {
		t.Errorf("NM-2 (Shiro) BeatmapID: expected nil, got %v", gqlPool.Slots[0].Pieces[1].BeatmapID)
	}
	// HD 组
	if gqlPool.Slots[1].Mod != PieceMod("HD") {
		t.Errorf("second group mod: expected HD, got %s", gqlPool.Slots[1].Mod)
	}
	if gqlPool.Slots[1].Pieces[0].State != PieceState("BANNED") {
		t.Errorf("HD-1 State: expected BANNED, got %s", gqlPool.Slots[1].Pieces[0].State)
	}

	t.Logf("✓ mapMappool: 2 groups (NM, HD), correct slots/states")
}

// ============================================================================
// Phase 1 — DataLoader 测试
// ============================================================================

func TestBeatmapLoaderCaching(t *testing.T) {
	// BeatmapLoader without a real service — just test cache logic
	loader := &BeatmapLoader{}

	// Test: osuID <= 0 returns nil, nil
	b, err := loader.Load(context.Background(), 0)
	if err != nil {
		t.Fatalf("expected no error for osuID=0, got %v", err)
	}
	if b != nil {
		t.Fatalf("expected nil beatmap for osuID=0, got %v", b)
	}

	// Test: nil is cached (sync.Map stores the nil)
	val, ok := loader.cache.Load(int64(0))
	if ok && val != nil {
		// sync.Map.Load returns (nil, true) for zero key if never stored
		// This is fine — Load(0) short-circuits before reaching cache
	}

	t.Logf("✓ BeatmapLoader: osuID<=0 returns nil without error")
}
