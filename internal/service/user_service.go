package service

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// UserService handles user management operations.
type UserService struct {
	users       repository.UserRepository
	invalidator CacheInvalidator
	log         *zap.Logger
	sessions    authsession.Revoker
}

// NewUserService creates a new UserService. If invalidator is nil, a
// no-op implementation is used (cache entries will expire naturally).
func NewUserService(users repository.UserRepository, invalidator CacheInvalidator, sessionRevokers ...authsession.Revoker) *UserService {
	if invalidator == nil {
		invalidator = noopInvalidator{}
	}
	var sessions authsession.Revoker
	if len(sessionRevokers) > 0 {
		sessions = sessionRevokers[0]
	}
	return &UserService{users: users, invalidator: invalidator, log: zap.NewNop(), sessions: sessions}
}

func (s *UserService) WithLogger(log *zap.Logger) *UserService {
	if log != nil {
		s.log = log
	}
	return s
}

// Get returns a user by id.
func (s *UserService) Get(ctx context.Context, id bson.ObjectID) (*domain.User, error) {
	return s.users.ByID(ctx, id)
}

// GetByOsuID returns a user by osu! online id.
func (s *UserService) GetByOsuID(ctx context.Context, osuID int64) (*domain.User, error) {
	return s.users.ByOsuID(ctx, osuID)
}

// List returns a paginated list of non-banned users.
func (s *UserService) List(ctx context.Context, params paginate.Params) (paginate.Result[domain.User], error) {
	return s.users.List(ctx, params)
}

// UpdateRoles replaces the roles of a user.
func (s *UserService) UpdateRoles(ctx context.Context, id bson.ObjectID, roles []domain.UserRole) (*domain.User, error) {
	user, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	user.Roles = roles
	if err := s.users.Update(ctx, user); err != nil {
		s.log.Error("failed to update user", zap.Int64("osu_id", user.OnlineID), zap.Error(err))
		return nil, err
	}
	// Invalidate cached copy so the new roles are visible immediately.
	if err := s.invalidator.InvalidateUser(ctx, user.OnlineID); err != nil {
		s.log.Error("failed to invalidate user cache", zap.Int64("osu_id", user.OnlineID), zap.Error(err))
		return nil, fmt.Errorf("%w: update roles: %w", errs.ErrCacheSync, err)
	}
	s.log.Info("user roles updated", zap.Int64("osu_id", user.OnlineID), zap.Any("roles", roles))
	if err := s.revokeSessions(ctx, user.ID.Hex()); err != nil {
		return nil, err
	}
	return user, nil
}

// SetBanned updates the ban status of a user.
func (s *UserService) SetBanned(ctx context.Context, id bson.ObjectID, banned bool) (*domain.User, error) {
	user, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	user.IsBanned = banned
	if err := s.users.Update(ctx, user); err != nil {
		s.log.Error("failed to update user", zap.Int64("osu_id", user.OnlineID), zap.Error(err))
		return nil, err
	}
	// Invalidate cached copy so the ban status is visible immediately.
	if err := s.invalidator.InvalidateUser(ctx, user.OnlineID); err != nil {
		s.log.Error("failed to invalidate user cache", zap.Int64("osu_id", user.OnlineID), zap.Error(err))
		return nil, fmt.Errorf("%w: update ban status: %w", errs.ErrCacheSync, err)
	}
	s.log.Info("user ban status changed", zap.Int64("osu_id", user.OnlineID), zap.Bool("banned", banned))
	if err := s.revokeSessions(ctx, user.ID.Hex()); err != nil {
		return nil, err
	}
	return user, nil
}

// SetVerifyStatus updates the verification status of a user.
func (s *UserService) SetVerifyStatus(ctx context.Context, id bson.ObjectID, status domain.VerifyStatus) (*domain.User, error) {
	user, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	switch status {
	case domain.Verified, domain.Pending, domain.Unverified:
		user.VerifyStatus = status
	default:
		return nil, fmt.Errorf("%w: invalid verify status", errs.ErrInvalidInput)
	}
	if err := s.users.Update(ctx, user); err != nil {
		s.log.Error("failed to update user", zap.Int64("osu_id", user.OnlineID), zap.Error(err))
		return nil, err
	}
	// Invalidate cached copy so the new verify status is visible immediately.
	if err := s.invalidator.InvalidateUser(ctx, user.OnlineID); err != nil {
		s.log.Error("failed to invalidate user cache", zap.Int64("osu_id", user.OnlineID), zap.Error(err))
		return nil, fmt.Errorf("%w: update verification status: %w", errs.ErrCacheSync, err)
	}
	s.log.Info("user verify status changed", zap.Int64("osu_id", user.OnlineID), zap.String("status", string(status)))
	if err := s.revokeSessions(ctx, user.ID.Hex()); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *UserService) revokeSessions(ctx context.Context, userID string) error {
	if s.sessions == nil {
		return nil
	}
	if err := s.sessions.RevokeUser(ctx, userID); err != nil {
		return fmt.Errorf("%w: revoke browser sessions: %w", errs.ErrCacheSync, err)
	}
	return nil
}
