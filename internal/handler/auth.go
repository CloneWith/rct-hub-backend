package handler

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// AuthHandler exposes osu! OAuth endpoints.
type AuthHandler struct {
	authService service.AuthService
	sessions    authsession.Manager
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

func NewAuthHandler(authService service.AuthService, sessions authsession.Manager, frontEndURI string, configs ...AuthCookieConfig) *AuthHandler {
	cookie := AuthCookieConfig{Name: "rcthub_session", SameSite: http.SameSiteLaxMode, TTL: 7 * 24 * time.Hour}
	if len(configs) > 0 {
		cookie = configs[0]
	}
	return &AuthHandler{authService: authService, sessions: sessions, frontEndURI: strings.TrimRight(frontEndURI, "/"), cookie: cookie}
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
// issues a revocable opaque HttpOnly session cookie, and redirects without putting
// the credential in browser history, logs, or referrer headers.
func (h *AuthHandler) OsuCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	if code == "" {
		response.BadRequest(c, "missing authorization code")
		return
	}

	_, user, err := h.authService.Callback(c.Request.Context(), code, state)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if h.sessions == nil {
		response.InternalError(c, "browser session store is unavailable")
		return
	}

	secret, err := h.sessions.Create(c.Request.Context(), user)
	if err != nil {
		response.InternalError(c, "failed to create browser session")
		return
	}
	h.setSessionCookie(c, secret, int(h.cookie.TTL.Seconds()))
	redirect, err := url.Parse(h.frontEndURI + "/auth/callback")
	if err != nil {
		response.InternalError(c, "invalid frontend redirect")
		return
	}
	c.Redirect(http.StatusSeeOther, redirect.String())
}

func (h *AuthHandler) Logout(c *gin.Context) {
	secret, _ := c.Cookie(h.cookie.Name)
	h.setSessionCookie(c, "", -1)
	if h.sessions == nil {
		response.InternalError(c, "browser session store is unavailable")
		return
	}
	if err := h.sessions.Revoke(c.Request.Context(), secret); err != nil {
		response.InternalError(c, "failed to revoke browser session")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) setSessionCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: h.cookie.Name, Value: value, Path: "/", Domain: h.cookie.Domain,
		MaxAge: maxAge, HttpOnly: true, Secure: h.cookie.Secure, SameSite: h.cookie.SameSite,
	})
}
