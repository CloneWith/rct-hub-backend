package service

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// UserService handles user management operations.
type UserService struct {
	users       repository.UserRepository
	invalidator CacheInvalidator
}

// NewUserService creates a new UserService. If invalidator is nil, a
// no-op implementation is used (cache entries will expire naturally).
func NewUserService(users repository.UserRepository, invalidator CacheInvalidator) *UserService {
	if invalidator == nil {
		invalidator = noopInvalidator{}
	}
	return &UserService{users: users, invalidator: invalidator}
}

// Get returns a user by id.
func (s *UserService) Get(ctx context.Context, id bson.ObjectID) (*domain.User, error) {
	return s.users.ByID(ctx, id)
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
		return nil, err
	}
	// Invalidate cached copy so the new roles are visible immediately.
	_ = s.invalidator.InvalidateUser(ctx, user.OnlineID)
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
		return nil, err
	}
	// Invalidate cached copy so the ban status is visible immediately.
	_ = s.invalidator.InvalidateUser(ctx, user.OnlineID)
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
		return nil, err
	}
	// Invalidate cached copy so the new verify status is visible immediately.
	_ = s.invalidator.InvalidateUser(ctx, user.OnlineID)
	return user, nil
}
