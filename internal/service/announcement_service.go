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

// AnnouncementPatch is a partial update request for an announcement. Only
// non-nil fields are applied; omitted fields keep their current values.
type AnnouncementPatch struct {
	Title   *string `json:"title,omitempty"`
	Content *string `json:"content,omitempty"`
	Pinned  *bool   `json:"pinned,omitempty"`
	Visible *bool   `json:"visible,omitempty"`
}

// Patch applies a partial update to an existing announcement. The author id is
// preserved and never changed through this endpoint.
func (s *AnnouncementService) Patch(ctx context.Context, id bson.ObjectID, patch *AnnouncementPatch) (*domain.Announcement, error) {
	a, err := s.announcements.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if patch.Title != nil {
		a.Title = *patch.Title
	}
	if patch.Content != nil {
		a.Content = *patch.Content
	}
	if patch.Pinned != nil {
		a.Pinned = *patch.Pinned
	}
	if patch.Visible != nil {
		a.Visible = *patch.Visible
	}
	if err := s.announcements.Update(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
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
