package middleware

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/jwtutil"
	"rctHubBackend/pkg/response"
)

const (
	ContextKeyClaims = "claims"
	ContextKeyUserID = "user_id"
)

// Auth verifies the JWT in the Authorization header.
func Auth(signer *jwtutil.Signer) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			response.Unauthorized(c, "missing authorization header")
			c.Abort()
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Unauthorized(c, "invalid authorization header")
			c.Abort()
			return
		}

		claims, err := signer.Parse(parts[1])
		if err != nil {
			response.Error(c, http.StatusUnauthorized, "invalid or expired token")
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
