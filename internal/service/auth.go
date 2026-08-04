package service

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/oauth"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/jwtutil"
)

// AuthService handles osu! OAuth login and local session creation.
type AuthService interface {
	BeginOAuth(ctx context.Context) (string, error)
	Callback(ctx context.Context, code, state string) (string, *domain.User, error)
	Me(ctx context.Context, userID string) (*domain.User, error)
}

type authService struct {
	oauthClient oauth.OAuthClient
	userRepo    repository.UserRepository
	invalidator CacheInvalidator
	signer      *jwtutil.Signer
	jwtExpiry   time.Duration
}

// NewAuthService creates a new AuthService.
func NewAuthService(
	oauthClient oauth.OAuthClient,
	userRepo repository.UserRepository,
	invalidator CacheInvalidator,
	signer *jwtutil.Signer,
	jwtExpiry time.Duration,
) AuthService {
	if invalidator == nil {
		invalidator = noopInvalidator{}
	}
	return &authService{
		oauthClient: oauthClient,
		userRepo:    userRepo,
		invalidator: invalidator,
		signer:      signer,
		jwtExpiry:   jwtExpiry,
	}
}

func (s *authService) BeginOAuth(ctx context.Context) (string, error) {
	return s.oauthClient.AuthURL(ctx)
}

func (s *authService) Callback(ctx context.Context, code, state string) (string, *domain.User, error) {
	token, err := s.oauthClient.Exchange(ctx, code, state)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", errs.ErrUnauthorized, err)
	}

	osuUser, err := s.oauthClient.Me(ctx, token)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", errs.ErrUnauthorized, err)
	}

	user, err := s.userRepo.UpsertOsuFields(ctx, osuUser.ID, bson.M{
		"username":     osuUser.Username,
		"avatar_url":   osuUser.AvatarURL,
		"country_code": osuUser.Country.Code,
	})
	if err != nil {
		return "", nil, err
	}
	if err := s.invalidator.InvalidateUser(ctx, user.OnlineID); err != nil {
		return "", nil, fmt.Errorf("%w: oauth login: %w", errs.ErrCacheSync, err)
	}

	if user.IsBanned {
		return "", nil, errs.ErrForbidden
	}

	jwtToken, err := s.signer.Generate(user.ID.Hex(), user.OnlineID, user.Username, user.Roles, s.jwtExpiry)
	if err != nil {
		return "", nil, fmt.Errorf("sign token: %w", err)
	}

	return jwtToken, user, nil
}

func (s *authService) Me(ctx context.Context, userID string) (*domain.User, error) {
	id, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errs.ErrInvalidInput
	}
	return s.userRepo.ByID(ctx, id)
}
