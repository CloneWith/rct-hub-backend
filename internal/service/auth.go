package service

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

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
	log         *zap.Logger
}

// NewAuthService creates a new AuthService.
func NewAuthService(
	oauthClient oauth.OAuthClient,
	userRepo repository.UserRepository,
	invalidator CacheInvalidator,
	signer *jwtutil.Signer,
	jwtExpiry time.Duration,
	log *zap.Logger,
) AuthService {
	if invalidator == nil {
		invalidator = noopInvalidator{}
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &authService{
		oauthClient: oauthClient,
		userRepo:    userRepo,
		invalidator: invalidator,
		signer:      signer,
		jwtExpiry:   jwtExpiry,
		log:         log,
	}
}

func (s *authService) BeginOAuth(ctx context.Context) (string, error) {
	s.log.Info("starting OAuth login flow")
	url, err := s.oauthClient.AuthURL(ctx)
	if err != nil {
		s.log.Error("failed to begin OAuth flow", zap.Error(err))
		return "", err
	}
	return url, nil
}

func (s *authService) Callback(ctx context.Context, code, state string) (string, *domain.User, error) {
	token, err := s.oauthClient.Exchange(ctx, code, state)
	if err != nil {
		s.log.Warn("OAuth callback: code exchange failed", zap.Error(err))
		return "", nil, fmt.Errorf("%w: %v", errs.ErrUnauthorized, err)
	}

	osuUser, err := s.oauthClient.Me(ctx, token)
	if err != nil {
		s.log.Warn("OAuth callback: failed to fetch osu! profile", zap.Error(err))
		return "", nil, fmt.Errorf("%w: %v", errs.ErrUnauthorized, err)
	}

	user, err := s.userRepo.UpsertOsuFields(ctx, osuUser.ID, bson.M{
		"username":     osuUser.Username,
		"avatar_url":   osuUser.AvatarURL,
		"country_code": osuUser.Country.Code,
	})
	if err != nil {
		s.log.Error("OAuth callback: failed to upsert user",
			zap.Int64("osu_id", osuUser.ID),
			zap.String("username", osuUser.Username),
			zap.Error(err),
		)
		return "", nil, err
	}
	if err := s.invalidator.InvalidateUser(ctx, user.OnlineID); err != nil {
		s.log.Error("OAuth callback: failed to invalidate user cache",
			zap.Int64("osu_id", user.OnlineID),
			zap.Error(err),
		)
		return "", nil, fmt.Errorf("%w: oauth login: %w", errs.ErrCacheSync, err)
	}

	if user.IsBanned {
		s.log.Warn("OAuth callback: login denied — user is banned",
			zap.Int64("osu_id", user.OnlineID),
			zap.String("username", user.Username),
		)
		return "", nil, errs.ErrForbidden
	}

	jwtToken, err := s.signer.Generate(user.ID.Hex(), user.OnlineID, user.Username, user.Roles, s.jwtExpiry)
	if err != nil {
		s.log.Error("OAuth callback: failed to sign JWT",
			zap.Int64("osu_id", user.OnlineID),
			zap.Error(err),
		)
		return "", nil, fmt.Errorf("sign token: %w", err)
	}

	s.log.Info("OAuth login successful",
		zap.Int64("osu_id", user.OnlineID),
		zap.String("username", user.Username),
		zap.String("user_id", user.ID.Hex()),
	)
	return jwtToken, user, nil
}

func (s *authService) Me(ctx context.Context, userID string) (*domain.User, error) {
	id, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errs.ErrInvalidInput
	}
	return s.userRepo.ByID(ctx, id)
}
