package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/domain"
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

func TestOAuthCallbackUsesHttpOnlyCookieWithoutURLToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuthHandler(authServiceStub{token: "secret-jwt"}, "https://web.example", AuthCookieConfig{
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
	if len(cookies) != 1 || cookies[0].Value != "secret-jwt" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", cookies)
	}
	if location := response.Header().Get("Location"); location == "" || containsToken(location) {
		t.Fatalf("JWT leaked through redirect: %q", location)
	}
}

func containsToken(value string) bool {
	for _, candidate := range []string{"secret-jwt", "token="} {
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
