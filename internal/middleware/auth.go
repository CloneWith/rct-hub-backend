package middleware

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/jwtutil"
	"rctHubBackend/pkg/response"
)

const (
	ContextKeyClaims = "claims"
	ContextKeyUserID = "user_id"
)

// Auth accepts script Bearer JWTs and revocable browser session cookies.
func Auth(signer *jwtutil.Signer, sessions authsession.Resolver, cookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := authsession.ClaimsFromRequest(c.Request, signer, sessions, cookieName)
		if err != nil {
			response.Error(c, http.StatusUnauthorized, "invalid or expired authentication")
			c.Abort()
			return
		}

		c.Set(ContextKeyClaims, claims)
		c.Set(ContextKeyUserID, claims.UserID)
		c.Next()
	}
}

// ClaimsFromContext extracts JWT claims from the Gin context.
func ClaimsFromContext(c *gin.Context) (*jwtutil.Claims, bool) {
	v, exists := c.Get(ContextKeyClaims)
	if !exists {
		return nil, false
	}
	claims, ok := v.(*jwtutil.Claims)
	return claims, ok
}

// RequireRole ensures the authenticated user has at least one of the required roles.
// It must be used after the Auth middleware.
func RequireRole(roles ...domain.UserRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromContext(c)
		if !ok {
			response.Unauthorized(c, "missing authentication")
			c.Abort()
			return
		}

		for _, required := range roles {
			if slices.Contains(claims.Roles, required) {
				c.Next()
				return
			}
		}

		response.Forbidden(c, "insufficient permissions")
		c.Abort()
	}
}
