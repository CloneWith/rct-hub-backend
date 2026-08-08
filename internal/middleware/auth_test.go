package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"rctHubBackend/pkg/jwtutil"
)

type browserSessionResolverStub struct {
	claims *jwtutil.Claims
}

func (s browserSessionResolverStub) Resolve(context.Context, string) (*jwtutil.Claims, error) {
	return s.claims, nil
}

func TestAuthAcceptsBrowserSessionCookieForProtectedREST(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer := jwtutil.NewSigner("this-is-a-32-byte-secret-key-for-test!", "test")
	sessions := browserSessionResolverStub{claims: &jwtutil.Claims{UserID: "user-id", OsuID: 1001, Username: "captain"}}
	router := gin.New()
	router.GET("/protected", Auth(signer, sessions, "rcthub_session"), func(c *gin.Context) {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.String(http.StatusOK, "%d", claims.OsuID)
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(&http.Cookie{Name: "rcthub_session", Value: "opaque-session"})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "1001" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}
