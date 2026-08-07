package handler

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// AuthHandler exposes osu! OAuth endpoints.
type AuthHandler struct {
	authService service.AuthService
	frontEndURI string
	cookie      AuthCookieConfig
}

type AuthCookieConfig struct {
	Name     string
	Domain   string
	Secure   bool
	SameSite http.SameSite
	TTL      time.Duration
}

func NewAuthHandler(authService service.AuthService, frontEndURI string, configs ...AuthCookieConfig) *AuthHandler {
	cookie := AuthCookieConfig{Name: "rcthub_session", SameSite: http.SameSiteLaxMode, TTL: 7 * 24 * time.Hour}
	if len(configs) > 0 {
		cookie = configs[0]
	}
	return &AuthHandler{authService: authService, frontEndURI: strings.TrimRight(frontEndURI, "/"), cookie: cookie}
}

// OsuLogin redirects the browser to the osu! OAuth authorization page.
func (h *AuthHandler) OsuLogin(c *gin.Context) {
	url, err := h.authService.BeginOAuth(c.Request.Context())
	if err != nil {
		response.InternalError(c, "failed to start osu! login")
		return
	}
	c.Redirect(http.StatusTemporaryRedirect, url)
}

// OsuCallback handles the OAuth callback, creates/updates the local user,
// issues a JWT-backed HttpOnly session cookie, and redirects without putting
// the credential in browser history, logs, or referrer headers.
func (h *AuthHandler) OsuCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	if code == "" {
		response.BadRequest(c, "missing authorization code")
		return
	}

	token, _, err := h.authService.Callback(c.Request.Context(), code, state)
	if err != nil {
		_ = c.Error(err)
		return
	}

	h.setSessionCookie(c, token, int(h.cookie.TTL.Seconds()))
	redirect, err := url.Parse(h.frontEndURI + "/auth/callback")
	if err != nil {
		response.InternalError(c, "invalid frontend redirect")
		return
	}
	c.Redirect(http.StatusSeeOther, redirect.String())
}

func (h *AuthHandler) Logout(c *gin.Context) {
	h.setSessionCookie(c, "", -1)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) setSessionCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: h.cookie.Name, Value: value, Path: "/", Domain: h.cookie.Domain,
		MaxAge: maxAge, HttpOnly: true, Secure: h.cookie.Secure, SameSite: h.cookie.SameSite,
	})
}
