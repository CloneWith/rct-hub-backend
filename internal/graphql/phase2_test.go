package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/jwtutil"
)

// ============================================================================
// Phase 2 测试: 客户端视图计算 + Directive 鉴权
// ============================================================================

// --- 测试辅助: 构建测试用 Match ---

func testMatchWithBoard() *Match {
	now := time.Now()
	redSide := TeamSideRed
	blueSide := TeamSideBlue
	banPhase := MatchPhaseBan
	activeTeam := TeamSideRed
	action := "ban"
	strategistID := 12345

	return &Match{
		ID:         "507f1f77bcf86cd799439011",
		Code:       "TEST001",
		Name:       "Test Match",
		RoomID:     "507f1f77bcf86cd799439012",
		Status:     MatchStatusActive,
		Phase:      &banPhase,
		ActiveTeam: &activeTeam,
		Board: &Board{
			Rows: 4,
			Cols: 4,
			Cells: [][]*BoardCell{
				{
					{Position: &Position{Row: 0, Col: 0}, Zone: BoardZoneHd, State: "empty"},
					{Position: &Position{Row: 0, Col: 1}, Zone: BoardZoneHd, State: "occupied", PieceID: ptrStr("HD-1"), TeamID: ptrStr("RED")},
					{Position: &Position{Row: 0, Col: 2}, Zone: BoardZoneDt, State: "empty"},
					{Position: &Position{Row: 0, Col: 3}, Zone: BoardZoneDt, State: "empty"},
				},
				{
					{Position: &Position{Row: 1, Col: 0}, Zone: BoardZoneHd, State: "empty"},
					{Position: &Position{Row: 1, Col: 1}, Zone: BoardZoneHd, State: "empty"},
					{Position: &Position{Row: 1, Col: 2}, Zone: BoardZoneDt, State: "occupied", PieceID: ptrStr("DT-1"), TeamID: ptrStr("BLUE")},
					{Position: &Position{Row: 1, Col: 3}, Zone: BoardZoneDt, State: "empty"},
				},
				{
					{Position: &Position{Row: 2, Col: 0}, Zone: BoardZoneHr, State: "empty"},
					{Position: &Position{Row: 2, Col: 1}, Zone: BoardZoneHr, State: "empty"},
					{Position: &Position{Row: 2, Col: 2}, Zone: BoardZoneNm, State: "empty"},
					{Position: &Position{Row: 2, Col: 3}, Zone: BoardZoneNm, State: "empty"},
				},
				{
					{Position: &Position{Row: 3, Col: 0}, Zone: BoardZoneHr, State: "empty"},
					{Position: &Position{Row: 3, Col: 1}, Zone: BoardZoneHr, State: "empty"},
					{Position: &Position{Row: 3, Col: 2}, Zone: BoardZoneNm, State: "empty"},
					{Position: &Position{Row: 3, Col: 3}, Zone: BoardZoneNm, State: "empty"},
				},
			},
		},
		Pool: &Mappool{
			Slots: []*PoolSlotGroup{
				{
					Mod: PieceModNm,
					Pieces: []*PoolSlot{
						{Mod: PieceModNm, Index: 1, State: PieceStateNormal},
						{Mod: PieceModNm, Index: 2, State: PieceStateBanned},
					},
				},
				{
					Mod: PieceModHd,
					Pieces: []*PoolSlot{
						{Mod: PieceModHd, Index: 1, State: PieceStatePicked},
						{Mod: PieceModHd, Index: 2, State: PieceStateNormal},
					},
				},
			},
		},
		Teams: &MatchTeams{
			Red:  &Team{ID: "t1", Side: redSide, Name: "Red", StrategistID: &strategistID},
			Blue: &Team{ID: "t2", Side: blueSide, Name: "Blue", StrategistID: ptrInt(67890)},
		},
		TurnState: &TurnState{
			Phase:      banPhase,
			Counter:    -2,
			ActiveTeam: &activeTeam,
			Action:     &action,
			StartedAt:  &now,
			TimeLimit:  ptrInt(60),
			BonusTime:  ptrInt(15),
		},
		Timer: &TimerState{
			StartedAt: &now,
			TimeLimit: 60,
			BonusTime: 15,
			IsPaused:  false,
		},
		CreatedAt: now,
	}
}

// --- StrategistView 测试 ---

func TestComputeStrategistView(t *testing.T) {
	match := testMatchWithBoard()

	// 用户 12345 是红队策略师
	view := computeStrategistView(match, 12345)

	if !view.IsMyTurn {
		t.Error("expected IsMyTurn=true for red strategist when activeTeam=RED")
	}
	if view.MyTeam == nil || *view.MyTeam != TeamSideRed {
		t.Error("expected MyTeam=RED")
	}

	// ban phase → allowedActions should include "ban"
	found := false
	for _, a := range view.AllowedActions {
		if a == "ban" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'ban' in allowedActions, got %v", view.AllowedActions)
	}

	// robberyInProgress should be false (action="ban")
	if view.RobberyInProgress {
		t.Error("expected RobberyInProgress=false when action=ban")
	}

	// selectablePoolSlots should include "NM-1" and "HD-2" (both NORMAL state)
	if len(view.SelectablePoolSlots) != 2 {
		t.Errorf("expected 2 selectable slots, got %d: %v", len(view.SelectablePoolSlots), view.SelectablePoolSlots)
	}

	// Timer should be populated
	if view.Timer == nil {
		t.Fatal("expected Timer to be non-nil")
	}
	if view.Timer.DurationSeconds != 60 {
		t.Errorf("expected DurationSeconds=60, got %d", view.Timer.DurationSeconds)
	}

	t.Logf("✓ StrategistView: isMyTurn=%v, myTeam=%v, allowed=%v, slots=%v, cells=%v",
		view.IsMyTurn, view.MyTeam, view.AllowedActions, view.SelectablePoolSlots, view.SelectableBoardCells)
}

func TestStrategistViewNotMyTurn(t *testing.T) {
	match := testMatchWithBoard()

	// 用户 67890 是蓝队策略师，当前红队回合
	view := computeStrategistView(match, 67890)

	if view.IsMyTurn {
		t.Error("expected IsMyTurn=false for blue strategist when activeTeam=RED")
	}
	if view.MyTeam == nil || *view.MyTeam != TeamSideBlue {
		t.Error("expected MyTeam=BLUE")
	}
}

func TestStrategistViewNotStrategist(t *testing.T) {
	match := testMatchWithBoard()

	// 用户 99999 不是任何队的策略师
	view := computeStrategistView(match, 99999)

	if view.IsMyTurn {
		t.Error("expected IsMyTurn=false for non-strategist")
	}
	if view.MyTeam != nil {
		t.Error("expected MyTeam=nil for non-strategist")
	}
}

// --- SpectatorView 测试 ---

func TestComputeSpectatorView(t *testing.T) {
	match := testMatchWithBoard()

	view := computeSpectatorView(match)

	if view.CurrentPhase != MatchPhaseBan {
		t.Errorf("expected CurrentPhase=BAN, got %s", view.CurrentPhase)
	}
	if view.ActiveTeam == nil || *view.ActiveTeam != TeamSideRed {
		t.Error("expected ActiveTeam=RED")
	}
	if view.TurnNumber == nil || *view.TurnNumber != -2 {
		t.Error("expected TurnNumber=-2")
	}

	// Board summary should have 4x4 cells
	if view.Board == nil || len(view.Board.Cells) != 4 {
		t.Fatal("expected Board with 4 rows")
	}

	// Cell (0,1) should have a piece (HD-1, occupied by red)
	cell := view.Board.Cells[0][1]
	if cell.Piece == nil {
		t.Fatal("expected piece at cell (0,1)")
	}
	if cell.Piece.Mod != PieceModHd {
		t.Errorf("expected mod=HD, got %s", cell.Piece.Mod)
	}
	if cell.Piece.Owner == nil || *cell.Piece.Owner != TeamSideRed {
		t.Error("expected owner=RED")
	}

	// Scores: red has 1 occupied cell, blue has 1
	if view.Scores.Red != 1 {
		t.Errorf("expected Red score=1, got %d", view.Scores.Red)
	}
	if view.Scores.Blue != 1 {
		t.Errorf("expected Blue score=1, got %d", view.Scores.Blue)
	}

	t.Logf("✓ SpectatorView: phase=%s, scores=R%d-B%d", view.CurrentPhase, view.Scores.Red, view.Scores.Blue)
}

// --- OverlayView 测试 ---

func TestComputeOverlayView(t *testing.T) {
	match := testMatchWithBoard()

	view := computeOverlayView(match)

	if view.Timer == nil {
		t.Fatal("expected Timer to be non-nil")
	}
	if view.Timer.IsPaused {
		t.Error("expected IsPaused=false")
	}

	// Board render data should have 4x4 cells
	if view.Board == nil || len(view.Board.Cells) != 4 {
		t.Fatal("expected Board with 4 rows")
	}

	// Cell (0,1) should have piece render (HD-1)
	cell := view.Board.Cells[0][1]
	if cell.Piece == nil {
		t.Fatal("expected piece render at cell (0,1)")
	}
	if cell.Piece.Mod != PieceModHd {
		t.Errorf("expected mod=HD, got %s", cell.Piece.Mod)
	}

	// lastEvent should be nil (WebSocket not implemented)
	if view.LastEvent != nil {
		t.Error("expected LastEvent=nil")
	}

	t.Logf("✓ OverlayView: timer.remaining=%d, isWarning=%v", view.Timer.RemainingSeconds, view.Timer.IsWarning)
}

// --- RefereeView 测试 ---

func TestComputeRefereeView(t *testing.T) {
	match := testMatchWithBoard()

	view := computeRefereeView(match)

	if view.Board == nil {
		t.Fatal("expected Board to be non-nil")
	}
	if view.Pool == nil {
		t.Fatal("expected Pool to be non-nil")
	}
	if view.Teams == nil {
		t.Fatal("expected Teams to be non-nil")
	}
	if view.TurnState == nil {
		t.Fatal("expected TurnState to be non-nil")
	}
	if view.Timer == nil {
		t.Fatal("expected Timer to be non-nil")
	}
	// auditLog and connectionStatus should be empty (not nil)
	if len(view.AuditLog) != 0 {
		t.Errorf("expected empty AuditLog, got %d entries", len(view.AuditLog))
	}
	if len(view.ConnectionStatus) != 0 {
		t.Errorf("expected empty ConnectionStatus, got %d entries", len(view.ConnectionStatus))
	}

	t.Logf("✓ RefereeView: board=%dx%d, pool groups=%d", view.Board.Rows, view.Board.Cols, len(view.Pool.Slots))
}

// --- SelectableCells 测试 ---

func TestComputeSelectableCells(t *testing.T) {
	match := testMatchWithBoard()

	cells := computeSelectableCells(match)

	// Available selectable pieces: NM-1 (free mod), HD-2 (restricted to HD zone)
	// Free mod can go anywhere → all empty cells are selectable
	// So all empty cells (14 out of 16, minus 2 occupied) should be selectable
	if len(cells) == 0 {
		t.Error("expected non-empty selectable cells")
	}

	t.Logf("✓ SelectableCells: %d cells selectable", len(cells))
}

// --- Directive 测试 ---

func TestRequireRoleUnauthorized(t *testing.T) {
	ctx := context.Background()
	// No claims in context
	called := false
	next := func(ctx context.Context) (any, error) {
		called = true
		return "result", nil
	}

	admin := false
	_, err := RequireRole(ctx, nil, next, UserRoleStrategist, &admin)

	if err == nil {
		t.Fatal("expected error for unauthenticated request")
	}
	if !strings.Contains(err.Error(), "AUTH_REQUIRED") {
		t.Errorf("expected AUTH_REQUIRED error, got: %v", err)
	}
	if called {
		t.Error("next should not be called when unauthorized")
	}

	t.Logf("✓ @requireRole blocks unauthenticated: %v", err)
}

func TestRequireRoleAuthorized(t *testing.T) {
	claims := &jwtutil.Claims{
		UserID:   "507f1f77bcf86cd799439011",
		OsuID:    12345,
		Username: "testuser",
		Roles:    []domain.UserRole{domain.RoleStrategist},
	}
	ctx := WithClaims(context.Background(), claims)

	called := false
	next := func(ctx context.Context) (any, error) {
		called = true
		return "result", nil
	}

	admin := false
	result, err := RequireRole(ctx, nil, next, UserRoleStrategist, &admin)

	if err != nil {
		t.Fatalf("expected no error for authorized user, got: %v", err)
	}
	if !called {
		t.Error("next should be called when authorized")
	}
	if result != "result" {
		t.Errorf("expected result='result', got %v", result)
	}

	t.Logf("✓ @requireRole allows authorized strategist")
}

func TestRequireRoleAdminOverride(t *testing.T) {
	claims := &jwtutil.Claims{
		UserID:   "507f1f77bcf86cd799439011",
		OsuID:    12345,
		Username: "admin",
		Roles:    []domain.UserRole{domain.RoleAdmin},
	}
	ctx := WithClaims(context.Background(), claims)

	called := false
	next := func(ctx context.Context) (any, error) {
		called = true
		return "ok", nil
	}

	// admin=true → ADMIN role should pass even though role=REFEREE
	admin := true
	_, err := RequireRole(ctx, nil, next, UserRoleReferee, &admin)

	if err != nil {
		t.Fatalf("expected admin override to pass, got: %v", err)
	}
	if !called {
		t.Error("next should be called with admin override")
	}

	t.Logf("✓ @requireRole admin override works")
}

func TestRequireRoleWrongRole(t *testing.T) {
	claims := &jwtutil.Claims{
		UserID:   "507f1f77bcf86cd799439011",
		OsuID:    12345,
		Username: "player",
		Roles:    []domain.UserRole{domain.RolePlayer},
	}
	ctx := WithClaims(context.Background(), claims)

	next := func(ctx context.Context) (any, error) {
		t.Error("next should not be called")
		return nil, nil
	}

	admin := false
	_, err := RequireRole(ctx, nil, next, UserRoleStrategist, &admin)

	if err == nil {
		t.Fatal("expected error for wrong role")
	}
	if !strings.Contains(err.Error(), "ACTION_NOT_ALLOWED") {
		t.Errorf("expected ACTION_NOT_ALLOWED, got: %v", err)
	}

	t.Logf("✓ @requireRole blocks wrong role: %v", err)
}

func TestPublicDirective(t *testing.T) {
	ctx := context.Background()
	called := false
	next := func(ctx context.Context) (any, error) {
		called = true
		return "public", nil
	}

	result, err := Public(ctx, nil, next)

	if err != nil {
		t.Fatalf("expected no error from @public, got: %v", err)
	}
	if !called {
		t.Error("next should always be called for @public")
	}
	if result != "public" {
		t.Errorf("expected result='public', got %v", result)
	}

	t.Logf("✓ @public is a no-op pass-through")
}

// --- Schema Introspection 测试 ---

func TestSchemaHasViewTypes(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	query := `{
		__type(name: "StrategistView") { name fields { name } }
	}`

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+escapeJSON(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	var resp struct {
		Data struct {
			Type struct {
				Name   string `json:"name"`
				Fields []struct {
					Name string `json:"name"`
				} `json:"fields"`
			} `json:"__type"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v\nbody: %s", err, rr.Body.String())
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	if resp.Data.Type.Name != "StrategistView" {
		t.Fatalf("expected StrategistView type, got %s", resp.Data.Type.Name)
	}

	fieldNames := map[string]bool{}
	for _, f := range resp.Data.Type.Fields {
		fieldNames[f.Name] = true
	}
	for _, expected := range []string{"isMyTurn", "myTeam", "allowedActions", "selectablePoolSlots", "timer"} {
		if !fieldNames[expected] {
			t.Errorf("missing field %s on StrategistView", expected)
		}
	}

	t.Logf("✓ Schema has StrategistView with %d fields", len(resp.Data.Type.Fields))
}

func TestSchemaHasRequireRoleDirective(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	// Standard GraphQL introspection doesn't expose directives on __Field.
	// We verify field existence here; directive enforcement is tested by
	// TestRequireRoleUnauthorized/Authorized/AdminOverride/WrongRole.
	query := `{
		__type(name: "Match") {
			fields {
				name
			}
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+escapeJSON(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	var resp struct {
		Data struct {
			Type struct {
				Fields []struct {
					Name string `json:"name"`
				} `json:"fields"`
			} `json:"__type"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v\nbody: %s", err, rr.Body.String())
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("introspection returned errors: %v", resp.Errors)
	}

	// Verify all 4 view fields exist on Match type
	expectedFields := map[string]bool{
		"strategistView": false,
		"spectatorView":  false,
		"overlayView":    false,
		"refereeView":    false,
	}
	for _, f := range resp.Data.Type.Fields {
		if _, ok := expectedFields[f.Name]; ok {
			expectedFields[f.Name] = true
		}
	}
	for field, found := range expectedFields {
		if !found {
			t.Errorf("expected %s field on Match type", field)
		}
	}
	if !expectedFields["strategistView"] {
		t.Fatal("strategistView field not found in Match type")
	}
	t.Logf("✓ Match type has all 4 view fields (strategistView, spectatorView, overlayView, refereeView)")
}

// --- 辅助函数 ---

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
