package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// AuthHandler exposes osu! OAuth and session endpoints.
type AuthHandler struct {
	authService service.AuthService
}

func NewAuthHandler(authService service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
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
// issues a JWT, and redirects back to the frontend with the token.
func (h *AuthHandler) OsuCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	if code == "" {
		response.BadRequest(c, "missing authorization code")
		return
	}

	token, _, err := h.authService.Callback(c.Request.Context(), code, state)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, err.Error())
		return
	}

	// Redirect to the frontend with the JWT in the URL fragment.
	// The frontend is responsible for storing the token securely.
	c.Redirect(http.StatusTemporaryRedirect, "/auth/callback?token="+token)
}

// Me returns the currently authenticated user.
func (h *AuthHandler) Me(c *gin.Context) {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		response.Unauthorized(c, "missing authentication")
		return
	}

	user, err := h.authService.Me(c.Request.Context(), claims.UserID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.JSON(c, user)
}
