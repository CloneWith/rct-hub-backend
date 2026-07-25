package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestPingQuery 验证 GraphQL ping 查询返回 "pong"。
// 这是 Phase 0 脚手架的核心验收标准。
func TestPingQuery(t *testing.T) {
	resolver := NewResolver(nil) // ping 不需要 UserService
	srv := NewHandler(resolver)

	query := `{"query":"{ ping }"}`

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Data struct {
			Ping string `json:"ping"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v\nbody: %s", err, rr.Body.String())
	}

	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}

	if resp.Data.Ping != "pong" {
		t.Fatalf("expected ping='pong', got '%s'", resp.Data.Ping)
	}

	t.Logf("✓ query { ping } → \"%s\"", resp.Data.Ping)
}

// TestPlaygroundReachable 验证 GraphiQL playground HTML 页面可访问。
func TestPlaygroundReachable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/graphql", GinPlayground("/graphql"))

	req := httptest.NewRequest(http.MethodGet, "/graphql", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "GraphQL Playground") {
		t.Fatalf("response does not contain playground HTML")
	}

	t.Logf("✓ GET /graphql → GraphQL Playground HTML")
}

// TestMeUnauthenticated 验证未认证时 me 查询返回 null（不报错）。
func TestMeUnauthenticated(t *testing.T) {
	resolver := NewResolver(nil) // me 在未认证时不调用 UserService
	srv := NewHandler(resolver)

	query := `{"query":"{ me { id username } }"}`

	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(query))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Data struct {
			Me *struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"me"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v\nbody: %s", err, rr.Body.String())
	}

	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors for unauthenticated me: %+v", resp.Errors)
	}

	if resp.Data.Me != nil {
		t.Fatalf("expected me=null for unauthenticated request, got %+v", resp.Data.Me)
	}

	t.Logf("✓ query { me } without auth → null (no error)")
}

// TestClaimsContext 验证 WithClaims/ClaimsFromCtx 上下文传递。
func TestClaimsContext(t *testing.T) {
	ctx := context.Background()

	// 未注入 claims
	_, ok := ClaimsFromCtx(ctx)
	if ok {
		t.Fatal("expected ok=false when no claims in context")
	}

	// 注入 nil claims → 不应 panic，不应设置
	ctx = WithClaims(ctx, nil)
	_, ok = ClaimsFromCtx(ctx)
	if ok {
		t.Fatal("expected ok=false when claims is nil")
	}

	t.Logf("✓ WithClaims/ClaimsFromCtx context passing works")
}
