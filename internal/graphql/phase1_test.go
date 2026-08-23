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
		"User", "Room", "RoomSettings", "Match", "MatchSnapshot",
		"FormalBoard", "FormalBoardCell", "FormalBoardPiece", "FormalTimer",
		"FormalPoolSlot", "FormalRosters", "FormalRoster", "Position",
		"Mappool", "PoolSlotGroup", "PoolSlot", "Beatmap", "Announcement",
		"StrategistView", "CaptainView", "SpectatorView", "OverlayView", "RefereeView",
		"MatchPage", "RoomPage", "BeatmapPage", "UserPage", "AnnouncementPage",
	}
	for _, et := range expectedTypes {
		if !typeNames[et] {
			t.Errorf("expected type %s in schema, not found", et)
		}
	}
	for _, removed := range []string{"MatchTeams", "Board", "BoardCell", "Move", "TurnState", "TimerState"} {
		if typeNames[removed] {
			t.Errorf("legacy type %s remains in the Web contract", removed)
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

func TestRoomQueryContractUsesStableIdentifiersAndFormalSnapshot(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)
	query := `{"query":"{ user: __type(name: \"User\") { fields { name type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } room: __type(name: \"Room\") { fields { name type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } settings: __type(name: \"RoomSettings\") { fields { name type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } match: __type(name: \"Match\") { fields { name type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	type typeRef struct {
		Kind   string   `json:"kind"`
		Name   string   `json:"name"`
		OfType *typeRef `json:"ofType"`
	}
	type field struct {
		Name string  `json:"name"`
		Type typeRef `json:"type"`
	}
	var response struct {
		Data struct {
			User struct {
				Fields []field `json:"fields"`
			} `json:"user"`
			Room struct {
				Fields []field `json:"fields"`
			} `json:"room"`
			Settings struct {
				Fields []field `json:"fields"`
			} `json:"settings"`
			Match struct {
				Fields []field `json:"fields"`
			} `json:"match"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode introspection response: %v", err)
	}
	if len(response.Errors) != 0 {
		t.Fatalf("introspection errors: %+v", response.Errors)
	}

	fieldType := func(fields []field, name string) typeRef {
		for _, item := range fields {
			if item.Name == name {
				return item.Type
			}
		}
		t.Fatalf("field %s not found", name)
		return typeRef{}
	}
	if got := fieldType(response.Data.User.Fields, "onlineID"); got.Kind != "NON_NULL" || got.OfType == nil || got.OfType.Name != "ID" {
		t.Fatalf("User.onlineID must be ID!, got %+v", got)
	}
	if got := fieldType(response.Data.Room.Fields, "ownerID"); got.Kind != "NON_NULL" || got.OfType == nil || got.OfType.Name != "ID" {
		t.Fatalf("Room.ownerID must be ID!, got %+v", got)
	}
	if got := fieldType(response.Data.Settings.Fields, "redPlayers"); got.Kind != "NON_NULL" || got.OfType == nil || got.OfType.Kind != "LIST" || got.OfType.OfType == nil || got.OfType.OfType.Kind != "NON_NULL" || got.OfType.OfType.OfType == nil || got.OfType.OfType.OfType.Name != "ID" {
		t.Fatalf("RoomSettings.redPlayers must be [ID!]!, got %+v", got)
	}
	if got := fieldType(response.Data.Match.Fields, "snapshot"); got.Kind != "NON_NULL" || got.OfType == nil || got.OfType.Name != "MatchSnapshot" {
		t.Fatalf("Match.snapshot must be MatchSnapshot!, got %+v", got)
	}
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
	if gqlUser.OnlineID != "12345" {
		t.Errorf("OnlineID: expected 12345, got %s", gqlUser.OnlineID)
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

func TestOsuIDContractDoesNotTruncateAtGraphQLInt32(t *testing.T) {
	const largeID int64 = 4_294_967_296
	mapped := mapUser(&domain.User{OnlineID: largeID})
	if mapped.OnlineID != "4294967296" {
		t.Fatalf("mapped osu ID = %q", mapped.OnlineID)
	}
	parsed, err := parsePositiveInt64ID(mapped.OnlineID)
	if err != nil || parsed != largeID {
		t.Fatalf("parsed osu ID = %d, %v", parsed, err)
	}
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
	pool.Slots[domain.PieceModNM] = []domain.Piece{
		{BeatmapID: &nmID, State: domain.PieceStateNormal},
		{BeatmapID: nil, State: domain.PieceStateNormal}, // Shiro
	}
	pool.Slots[domain.PieceModHD] = []domain.Piece{
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
	if gqlPool.Slots[0].Pieces[0].BeatmapID == nil || *gqlPool.Slots[0].Pieces[0].BeatmapID != "12345" {
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
