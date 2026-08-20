package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/pkg/jwtutil"
)

type browserSessionResolverStub struct {
	claims *jwtutil.Claims
}

func (s browserSessionResolverStub) Resolve(context.Context, string) (*jwtutil.Claims, error) {
	return s.claims, nil
}

// renewingSessionResolverStub implements RenewalResolver and reports whether the
// session was slid, mirroring the real authsession.Store.
type renewingSessionResolverStub struct {
	claims  *jwtutil.Claims
	renewed bool
}

func (s renewingSessionResolverStub) Resolve(context.Context, string) (*jwtutil.Claims, error) {
	return s.claims, nil
}

func (s renewingSessionResolverStub) ResolveWithRenewal(context.Context, string) (*jwtutil.Claims, bool, error) {
	return s.claims, s.renewed, nil
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

func TestAuthRefreshesCookieWhenSessionIsRenewed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer := jwtutil.NewSigner("this-is-a-32-byte-secret-key-for-test!", "test")
	sessions := renewingSessionResolverStub{claims: &jwtutil.Claims{UserID: "user-id", OsuID: 1001, Username: "captain"}, renewed: true}
	cookie := authsession.CookieConfig{
		Name: "rcthub_session", Domain: "example.com", Secure: true,
		SameSite: http.SameSiteStrictMode, TTL: 24 * time.Hour,
	}
	router := gin.New()
	router.GET("/protected", Auth(signer, sessions, cookie.Name, cookie), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(&http.Cookie{Name: cookie.Name, Value: "opaque-session"})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	setCookies := response.Result().Cookies()
	if len(setCookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want exactly one refreshed cookie", len(setCookies))
	}
	refreshed := setCookies[0]
	if refreshed.Name != cookie.Name || refreshed.Value != "opaque-session" {
		t.Fatalf("refreshed cookie = %+v", refreshed)
	}
	if refreshed.MaxAge != int((24 * time.Hour).Seconds()) {
		t.Fatalf("MaxAge = %d, want %d", refreshed.MaxAge, int((24 * time.Hour).Seconds()))
	}
	if !refreshed.HttpOnly || !refreshed.Secure || refreshed.SameSite != http.SameSiteStrictMode || refreshed.Domain != "example.com" {
		t.Fatalf("refreshed cookie attributes = %+v", refreshed)
	}
}

func TestAuthDoesNotRefreshCookieWithoutRenewal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer := jwtutil.NewSigner("this-is-a-32-byte-secret-key-for-test!", "test")
	sessions := renewingSessionResolverStub{claims: &jwtutil.Claims{UserID: "user-id", OsuID: 1001, Username: "captain"}, renewed: false}
	cookie := authsession.CookieConfig{Name: "rcthub_session", TTL: 24 * time.Hour}
	router := gin.New()
	router.GET("/protected", Auth(signer, sessions, cookie.Name, cookie), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(&http.Cookie{Name: cookie.Name, Value: "opaque-session"})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("unexpected Set-Cookie without renewal: %+v", cookies)
	}
}

func TestAuthNeverRefreshesCookieForBearerTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer := jwtutil.NewSigner("this-is-a-32-byte-secret-key-for-test!", "test")
	sessions := renewingSessionResolverStub{claims: &jwtutil.Claims{UserID: "user-id", OsuID: 1001, Username: "captain"}, renewed: true}
	cookie := authsession.CookieConfig{Name: "rcthub_session", TTL: 24 * time.Hour}
	router := gin.New()
	router.GET("/protected", Auth(signer, sessions, cookie.Name, cookie), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	token, _ := signer.Generate("user-id", 1001, "captain", nil, time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("unexpected Set-Cookie for Bearer auth: %+v", cookies)
	}
}
