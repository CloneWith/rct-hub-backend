package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/jwtutil"
)

type authServiceStub struct {
	token string
}

func (s authServiceStub) BeginOAuth(context.Context) (string, error) {
	return "https://osu.example/authorize", nil
}
func (s authServiceStub) Callback(context.Context, string, string) (string, *domain.User, error) {
	return s.token, &domain.User{}, nil
}
func (s authServiceStub) Me(context.Context, string) (*domain.User, error) { return nil, nil }

type sessionManagerStub struct {
	secret  string
	revoked string
}

func (s *sessionManagerStub) Create(context.Context, *domain.User) (string, error) {
	return s.secret, nil
}
func (s *sessionManagerStub) Resolve(context.Context, string) (*jwtutil.Claims, error) {
	return nil, nil
}
func (s *sessionManagerStub) Revoke(_ context.Context, secret string) error {
	s.revoked = secret
	return nil
}
func (s *sessionManagerStub) RevokeUser(context.Context, string) error { return nil }

func TestOAuthCallbackUsesHttpOnlyCookieWithoutURLToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sessions := &sessionManagerStub{secret: "opaque-session"}
	handler := NewAuthHandler(authServiceStub{token: "secret-jwt"}, sessions, "https://web.example", AuthCookieConfig{
		Name: "rcthub_session", Secure: true, SameSite: http.SameSiteLaxMode, TTL: time.Hour,
	})
	request := httptest.NewRequest(http.MethodGet, "/auth/osu/callback?code=code&state=state", nil)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request

	handler.OsuCallback(ctx)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "https://web.example/auth/callback" {
		t.Fatalf("callback response = %d location=%q", response.Code, response.Header().Get("Location"))
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != "opaque-session" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", cookies)
	}
	if location := response.Header().Get("Location"); location == "" || containsToken(location) {
		t.Fatalf("JWT leaked through redirect: %q", location)
	}
}

func TestLogoutRevokesServerSessionAndClearsCookie(t *testing.T) {
	sessions := &sessionManagerStub{secret: "opaque-session"}
	handler := NewAuthHandler(authServiceStub{}, sessions, "https://web.example", AuthCookieConfig{Name: "rcthub_session", TTL: time.Hour})
	request := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	request.AddCookie(&http.Cookie{Name: "rcthub_session", Value: "opaque-session"})
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = request

	handler.Logout(ctx)

	if ctx.Writer.Status() != http.StatusNoContent || sessions.revoked != "opaque-session" {
		t.Fatalf("logout status=%d revoked=%q", ctx.Writer.Status(), sessions.revoked)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("logout cookie = %+v", cookies)
	}
}

func containsToken(value string) bool {
	for _, candidate := range []string{"secret-jwt", "opaque-session", "token="} {
		if len(value) >= len(candidate) {
			for index := 0; index+len(candidate) <= len(value); index++ {
				if value[index:index+len(candidate)] == candidate {
					return true
				}
			}
		}
	}
	return false
}
