package graphql

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rctHubBackend/pkg/jwtutil"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"
)

// ============================================================================
// Phase 3 测试: Command Mutations + 错误映射 + 认证
// ============================================================================

// --- 错误映射测试 ---

func TestMapError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"forbidden", errs.ErrForbidden, "ACTION_NOT_ALLOWED"},
		{"invalid input", errs.ErrInvalidInput, "INVALID_INPUT"},
		{"not found", errs.ErrNotFound, "NOT_FOUND"},
		{"already exists", errs.ErrAlreadyExists, "ALREADY_EXISTS"},
		{"unauthorized", errs.ErrUnauthorized, "AUTH_REQUIRED"},
		{"wrapped forbidden", wrapErr(errs.ErrForbidden, "not your turn"), "ACTION_NOT_ALLOWED"},
		{"wrapped invalid", wrapErr(errs.ErrInvalidInput, "piece not found"), "INVALID_INPUT"},
		{"generic error", errGeneric("something broke"), "INTERNAL"},
		{"nil error", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			me := mapError(tt.err)
			if tt.err == nil {
				if me != nil {
					t.Errorf("expected nil MatchError for nil error")
				}
				return
			}
			if me == nil {
				t.Fatalf("expected non-nil MatchError")
			}
			if me.Code != tt.wantCode {
				t.Errorf("code: got %s, want %s", me.Code, tt.wantCode)
			}
			if me.Message == "" {
				t.Error("expected non-empty message")
			}
		})
	}
}

func TestMapErrorStripsPrefix(t *testing.T) {
	// wrapped errors like "invalid input: piece not found" should strip prefix
	me := mapError(wrapErr(errs.ErrInvalidInput, "piece not found"))
	if me.Code != "INVALID_INPUT" {
		t.Errorf("code: got %s, want INVALID_INPUT", me.Code)
	}
	if me.Message != "piece not found" {
		t.Errorf("message: got %q, want %q", me.Message, "piece not found")
	}
}

func TestNotImplemented(t *testing.T) {
	me := notImplemented("undoAction")
	if me.Code != "NOT_IMPLEMENTED" {
		t.Errorf("code: got %s, want NOT_IMPLEMENTED", me.Code)
	}
	if !strings.Contains(me.Message, "undoAction") {
		t.Errorf("message should contain command name: %s", me.Message)
	}
}

// --- 认证测试 ---

func TestMutationRequiresAuth(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	// 无认证 → GraphQL error
	query := `mutation { advanceTurn(input: {matchId: "507f1f77bcf86cd799439011", expectedVersion: 1, commandId: "cmd1"}) { success } }`

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+escapeJSON(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	var resp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v\nbody: %s", err, rr.Body.String())
	}
	if len(resp.Errors) == 0 {
		t.Fatal("expected auth error, got none")
	}
	if !strings.Contains(resp.Errors[0].Message, "AUTH_REQUIRED") {
		t.Errorf("expected AUTH_REQUIRED error, got: %s", resp.Errors[0].Message)
	}
	t.Logf("✓ Mutation without auth → AUTH_REQUIRED")
}

// --- Schema 验证测试 ---

func TestSchemaHasMutationType(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	query := `{ __type(name: "Mutation") { fields { name } } }`

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
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v\nbody: %s", err, rr.Body.String())
	}

	expectedMutations := map[string]bool{
		"banPoolSlot":        false,
		"unbanPoolSlot":      false,
		"placePiece":         false,
		"grantWinPermission": false,
		"confirmPieceWinner": false,
		"beginRobbery":       false,
		"completeRobbery":    false,
		"cancelRobbery":      false,
		"declareTbWinner":    false,
		"declareSurrender":   false,
		"advanceTurn":        false,
		"pauseMatch":         false,
		"resumeMatch":        false,
		"undoAction":         false,
	}

	for _, f := range resp.Data.Type.Fields {
		if _, ok := expectedMutations[f.Name]; ok {
			expectedMutations[f.Name] = true
		}
	}

	for mut, found := range expectedMutations {
		if !found {
			t.Errorf("expected mutation %s not found in schema", mut)
		}
	}
	t.Logf("✓ Mutation type has all 14 mutations")
}

func TestSchemaHasInputTypes(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	// 检查关键 input 类型是否存在
	types := []string{"CommandMeta", "BanPoolSlotInput", "PlacePieceInput", "PoolSlotRef", "PositionInput", "UndoInput"}

	for _, typeName := range types {
		query := `{ __type(name: "` + typeName + `") { name kind } }`
		req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+escapeJSON(query)+`}`))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)

		var resp struct {
			Data struct {
				Type *struct {
					Name string `json:"name"`
					Kind string `json:"kind"`
				} `json:"__type"`
			} `json:"data"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode for %s: %v", typeName, err)
		}
		if resp.Data.Type == nil {
			t.Errorf("type %s not found in schema", typeName)
			continue
		}
		if resp.Data.Type.Kind != "INPUT_OBJECT" {
			t.Errorf("type %s: expected INPUT_OBJECT, got %s", typeName, resp.Data.Type.Kind)
		}
	}
	t.Logf("✓ All input types present (CommandMeta, BanPoolSlotInput, PlacePieceInput, etc.)")
}

func TestSchemaHasResultTypes(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	// 检查 result 类型和 interface
	types := []string{"CommandResult", "MatchError", "SimpleCommandResult", "BanPoolSlotResult", "PlacePieceResult", "CompleteRobberyResult"}

	for _, typeName := range types {
		query := `{ __type(name: "` + typeName + `") { name kind } }`
		req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+escapeJSON(query)+`}`))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)

		var resp struct {
			Data struct {
				Type *struct {
					Name string `json:"name"`
					Kind string `json:"kind"`
				} `json:"__type"`
			} `json:"data"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode for %s: %v", typeName, err)
		}
		if resp.Data.Type == nil {
			t.Errorf("type %s not found in schema", typeName)
		}
	}
	t.Logf("✓ All result types present (CommandResult interface, MatchError, SimpleCommandResult, etc.)")
}

func TestSchemaHas14Mutations(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	query := `{ __type(name: "Mutation") { fields { name } } }`

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
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp.Data.Type.Fields) != 14 {
		t.Errorf("expected 14 mutations, got %d", len(resp.Data.Type.Fields))
	}
	t.Logf("✓ Mutation type has exactly 14 fields")
}

// --- 辅助函数测试 ---

func TestPoolSlotRefToDomain(t *testing.T) {
	slot := poolSlotRefToDomain(PieceModNm, 1)
	if slot.Mod != domain.PieceModNM {
		t.Errorf("mod: got %s, want %s", slot.Mod, domain.PieceModNM)
	}
	if slot.Index != 1 {
		t.Errorf("index: got %d, want 1", slot.Index)
	}
}

func TestPositionInputToDomain(t *testing.T) {
	pos := positionInputToDomain(2, 3)
	if pos.X != 3 {
		t.Errorf("X: got %d, want 3 (col→X)", pos.X)
	}
	if pos.Y != 2 {
		t.Errorf("Y: got %d, want 2 (row→Y)", pos.Y)
	}
}

func TestTeamSideFromGraphQL(t *testing.T) {
	tests := []struct {
		input TeamSide
		want  domain.TeamSide
	}{
		{TeamSideRed, domain.TeamSideRed},
		{TeamSideBlue, domain.TeamSideBlue},
	}
	for _, tt := range tests {
		got := teamSideFromGraphQL(tt.input)
		if got != tt.want {
			t.Errorf("teamSideFromGraphQL(%s): got %s, want %s", tt.input, got, tt.want)
		}
	}
}

func TestEmptyEvents(t *testing.T) {
	events := emptyEvents()
	if events == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

// --- 带认证的 mutation 集成测试 (nil service → 错误映射) ---

func TestMutationWithAuthInvalidMatchID(t *testing.T) {
	// 使用 nil service 的 resolver — 不会到达 service 调用
	// 因为 matchID 解析在 service 调用之前
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	claims := &jwtutil.Claims{
		UserID: "507f1f77bcf86cd799439099",
		OsuID:  12345,
		Roles:  []domain.UserRole{domain.RoleStrategist},
	}

	query := `mutation { advanceTurn(input: {matchId: "invalid", expectedVersion: 1, commandId: "cmd1"}) { success error { code message } } }`

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+escapeJSON(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	var resp struct {
		Data struct {
			AdvanceTurn struct {
				Success bool `json:"success"`
				Error   *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"advanceTurn"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v\nbody: %s", err, rr.Body.String())
	}
	if resp.Data.AdvanceTurn.Success {
		t.Error("expected success=false for invalid match ID")
	}
	if resp.Data.AdvanceTurn.Error == nil {
		t.Fatal("expected error for invalid match ID")
	}
	if resp.Data.AdvanceTurn.Error.Code != "INVALID_INPUT" {
		t.Errorf("error code: got %s, want INVALID_INPUT", resp.Data.AdvanceTurn.Error.Code)
	}
	t.Logf("✓ advanceTurn with invalid matchID → success=false, error=INVALID_INPUT")
}

func TestMutationUnimplementedReturnsError(t *testing.T) {
	resolver := NewResolver(nil)
	srv := NewHandler(resolver)

	claims := &jwtutil.Claims{
		UserID: "507f1f77bcf86cd799439099",
		OsuID:  12345,
		Roles:  []domain.UserRole{domain.RoleAdmin},
	}

	// unbanPoolSlot 返回 NOT_IMPLEMENTED
	query := `mutation { unbanPoolSlot(input: {meta: {matchId: "507f1f77bcf86cd799439011", expectedVersion: 1, commandId: "cmd1"}, slot: {mod: NM, index: 1}}) { success error { code } } }`

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+escapeJSON(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithClaims(req.Context(), claims))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	var resp struct {
		Data struct {
			UnbanPoolSlot struct {
				Success bool `json:"success"`
				Error   *struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"unbanPoolSlot"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v\nbody: %s", err, rr.Body.String())
	}
	if resp.Data.UnbanPoolSlot.Success {
		t.Error("expected success=false for unimplemented mutation")
	}
	if resp.Data.UnbanPoolSlot.Error == nil {
		t.Fatal("expected error for unimplemented mutation")
	}
	if resp.Data.UnbanPoolSlot.Error.Code != "NOT_IMPLEMENTED" {
		t.Errorf("error code: got %s, want NOT_IMPLEMENTED", resp.Data.UnbanPoolSlot.Error.Code)
	}
	t.Logf("✓ unbanPoolSlot → NOT_IMPLEMENTED")
}

// --- 测试辅助 ---

type wrappedErr struct {
	base error
	msg  string
}

func (e *wrappedErr) Error() string { return e.base.Error() + ": " + e.msg }
func (e *wrappedErr) Unwrap() error { return e.base }

func wrapErr(base error, msg string) error {
	return &wrappedErr{base: base, msg: msg}
}

type genericErr string

func (e genericErr) Error() string { return string(e) }

func errGeneric(msg string) error { return genericErr(msg) }
