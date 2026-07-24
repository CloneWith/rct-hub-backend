package service

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/paginate"
)

// AnnouncementService handles announcement management operations.
type AnnouncementService struct {
	announcements repository.AnnouncementRepository
}

// NewAnnouncementService creates a new AnnouncementService.
func NewAnnouncementService(announcements repository.AnnouncementRepository) *AnnouncementService {
	return &AnnouncementService{announcements: announcements}
}

// Get returns an announcement by id.
func (s *AnnouncementService) Get(ctx context.Context, id bson.ObjectID) (*domain.Announcement, error) {
	return s.announcements.ByID(ctx, id)
}

// ListVisible returns visible announcements for public display.
func (s *AnnouncementService) ListVisible(ctx context.Context, params paginate.Params) (paginate.Result[domain.Announcement], error) {
	return s.announcements.ListVisible(ctx, params)
}

// ListAll returns all announcements including drafts (admin only).
func (s *AnnouncementService) ListAll(ctx context.Context, params paginate.Params) (paginate.Result[domain.Announcement], error) {
	return s.announcements.ListAll(ctx, params)
}

// Create creates a new announcement.
func (s *AnnouncementService) Create(ctx context.Context, a *domain.Announcement) error {
	return s.announcements.Create(ctx, a)
}

// Update updates an existing announcement.
func (s *AnnouncementService) Update(ctx context.Context, a *domain.Announcement) error {
	return s.announcements.Update(ctx, a)
}

// Delete removes an announcement.
func (s *AnnouncementService) Delete(ctx context.Context, id bson.ObjectID) error {
	return s.announcements.Delete(ctx, id)
}

// Publish marks an announcement as visible and sets the publish time.
func (s *AnnouncementService) Publish(ctx context.Context, id bson.ObjectID) (*domain.Announcement, error) {
	a, err := s.announcements.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	a.Visible = true
	now := time.Now().UTC()
	a.PublishedAt = &now
	if err := s.announcements.Update(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}
