package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// AuthHandler exposes osu! OAuth endpoints.
type AuthHandler struct {
	authService service.AuthService
	frontEndURI string
	log         *zap.Logger
}

func NewAuthHandler(authService service.AuthService, frontEndURI string, log *zap.Logger) *AuthHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &AuthHandler{authService: authService, frontEndURI: frontEndURI, log: log}
}

// OsuLogin redirects the browser to the osu! OAuth authorization page.
func (h *AuthHandler) OsuLogin(c *gin.Context) {
	h.log.Info("OAuth login initiated", zap.String("remote_addr", c.ClientIP()))
	url, err := h.authService.BeginOAuth(c.Request.Context())
	if err != nil {
		h.log.Error("failed to start OAuth login", zap.String("remote_addr", c.ClientIP()), zap.Error(err))
		response.InternalError(c, "failed to start osu! login")
		return
	}
	c.Redirect(http.StatusTemporaryRedirect, url)
}

// OsuCallback handles the OAuth callback, creates/updates the local user,
// issues a JWT, and redirects back to the frontend with the token.
func (h *AuthHandler) OsuCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	if code == "" {
		h.log.Warn("OAuth callback: missing authorization code", zap.String("remote_addr", c.ClientIP()))
		response.BadRequest(c, "missing authorization code")
		return
	}

	h.log.Info("OAuth callback received", zap.String("remote_addr", c.ClientIP()))
	token, _, err := h.authService.Callback(c.Request.Context(), code, state)
	if err != nil {
		h.log.Warn("OAuth callback: authentication failed", zap.String("remote_addr", c.ClientIP()), zap.Error(err))
		_ = c.Error(err)
		return
	}

	// Redirect to the frontend with the JWT in the URL fragment.
	// The frontend is responsible for storing the token securely.
	redirectLink := fmt.Sprintf("%s/auth/callback?token=%s", h.frontEndURI, token)
	c.Redirect(http.StatusTemporaryRedirect, redirectLink)
}
