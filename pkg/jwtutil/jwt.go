package jwtutil

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"rctHubBackend/internal/domain"
)

// Claims holds the data stored inside a JWT.
type Claims struct {
	UserID   string            `json:"user_id"`
	OsuID    int64             `json:"osu_id"`
	Username string            `json:"username"`
	Roles    []domain.UserRole `json:"roles"`
	jwt.RegisteredClaims
}

// Signer handles JWT signing and verification.
type Signer struct {
	secret []byte
	issuer string
}

func NewSigner(secret string, issuer string) *Signer {
	return &Signer{secret: []byte(secret), issuer: issuer}
}

// Generate creates a new JWT for a user.
func (s *Signer) Generate(userID string, osuID int64, username string, roles []domain.UserRole, expiry time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := Claims{
		UserID:   userID,
		OsuID:    osuID,
		Username: username,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    s.issuer,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// Parse validates a token string and returns its claims.
func (s *Signer) Parse(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}
