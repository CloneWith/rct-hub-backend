package service

import (
	"context"
	"errors"
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
	signer      *jwtutil.Signer
	jwtExpiry   time.Duration
}

// NewAuthService creates a new AuthService.
func NewAuthService(
	oauthClient oauth.OAuthClient,
	userRepo repository.UserRepository,
	signer *jwtutil.Signer,
	jwtExpiry time.Duration,
) AuthService {
	return &authService{
		oauthClient: oauthClient,
		userRepo:    userRepo,
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

	user, err := s.userRepo.ByOsuID(ctx, osuUser.ID)
	if err != nil {
		if !errors.Is(err, errs.ErrNotFound) {
			return "", nil, err
		}
		// Create a new user on first login.
		user = &domain.User{
			ID:           bson.NewObjectID(),
			OnlineID:     osuUser.ID,
			Username:     osuUser.Username,
			AvatarURL:    osuUser.AvatarURL,
			CountryCode:  osuUser.Country.Code,
			Roles:        []domain.UserRole{domain.RolePlayer},
			IsBanned:     false,
			VerifyStatus: domain.Pending,
		}
		if err := s.userRepo.Create(ctx, user); err != nil {
			return "", nil, err
		}
	} else {
		// Update profile metadata on each login.
		user.Username = osuUser.Username
		user.AvatarURL = osuUser.AvatarURL
		user.CountryCode = osuUser.Country.Code
		if err := s.userRepo.Update(ctx, user); err != nil {
			return "", nil, err
		}
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
