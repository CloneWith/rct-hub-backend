package middleware

import (
	"net/http"
	"strings"

	"rctHubBackend/pkg/jwtutil"
	"rctHubBackend/pkg/response"

	"github.com/gin-gonic/gin"
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
