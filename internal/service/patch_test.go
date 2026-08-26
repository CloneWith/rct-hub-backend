package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// fakeAnnouncementRepo is an in-memory announcement repository for tests.
type fakeAnnouncementRepo struct {
	byID map[bson.ObjectID]*domain.Announcement
}

func newFakeAnnouncementRepo() *fakeAnnouncementRepo {
	return &fakeAnnouncementRepo{byID: make(map[bson.ObjectID]*domain.Announcement)}
}

func (r *fakeAnnouncementRepo) Create(_ context.Context, a *domain.Announcement) error {
	r.byID[a.ID] = a
	return nil
}

func (r *fakeAnnouncementRepo) Update(_ context.Context, a *domain.Announcement) error {
	if _, ok := r.byID[a.ID]; !ok {
		return errs.ErrNotFound
	}
	r.byID[a.ID] = a
	return nil
}

func (r *fakeAnnouncementRepo) ByID(_ context.Context, id bson.ObjectID) (*domain.Announcement, error) {
	a, ok := r.byID[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return a, nil
}

func (r *fakeAnnouncementRepo) ListVisible(_ context.Context, _ paginate.Params) (paginate.Result[domain.Announcement], error) {
	return paginate.Result[domain.Announcement]{}, nil
}

func (r *fakeAnnouncementRepo) ListAll(_ context.Context, _ paginate.Params) (paginate.Result[domain.Announcement], error) {
	return paginate.Result[domain.Announcement]{}, nil
}

func (r *fakeAnnouncementRepo) Delete(_ context.Context, id bson.ObjectID) error {
	if _, ok := r.byID[id]; !ok {
		return errs.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}

var _ repository.AnnouncementRepository = (*fakeAnnouncementRepo)(nil)

func TestBeatmapServicePatchOnlyChangesProvidedFields(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	bid := bson.NewObjectID()
	bm := &domain.Beatmap{
		ID:       bid,
		OnlineID: 100,
		Title:    "Old Title",
		Artist:   "Old Artist",
		Status:   "ranked",
	}
	_ = repo.Create(ctx, bm)

	inv := &mockInvalidator{}
	svc := NewBeatmapService(repo, inv, nil)

	newTitle := "New Title"
	patched, err := svc.Patch(ctx, bid, &BeatmapPatch{Title: &newTitle})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if patched.Title != "New Title" {
		t.Errorf("title = %q, want %q", patched.Title, "New Title")
	}
	if patched.Artist != "Old Artist" {
		t.Errorf("artist changed unexpectedly to %q", patched.Artist)
	}
	if patched.Status != "ranked" {
		t.Errorf("status changed unexpectedly to %q", patched.Status)
	}
	if len(inv.beatmapCalls) != 1 || inv.beatmapCalls[0] != 100 {
		t.Errorf("expected InvalidateBeatmap(100), got %v", inv.beatmapCalls)
	}
}

func TestBeatmapServicePatchNotFoundDoesNotInvalidate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	inv := &mockInvalidator{}
	svc := NewBeatmapService(repo, inv, nil)

	newTitle := "New Title"
	_, err := svc.Patch(ctx, bson.NewObjectID(), &BeatmapPatch{Title: &newTitle})
	if err != errs.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if len(inv.beatmapCalls) != 0 {
		t.Errorf("expected no invalidation on failure, got %v", inv.beatmapCalls)
	}
}

func TestAnnouncementServicePatchOnlyChangesProvidedFields(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAnnouncementRepo()
	id := bson.NewObjectID()
	now := time.Now().UTC()
	a := &domain.Announcement{
		ID:        id,
		Title:     "Old Title",
		Content:   "Old Content",
		Pinned:    false,
		Visible:   false,
		AuthorID:  42,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = repo.Create(ctx, a)

	svc := NewAnnouncementService(repo)
	visible := true
	patched, err := svc.Patch(ctx, id, &AnnouncementPatch{Visible: &visible})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if !patched.Visible {
		t.Error("expected Visible to be true")
	}
	if patched.Title != "Old Title" {
		t.Errorf("title changed unexpectedly to %q", patched.Title)
	}
	if patched.Content != "Old Content" {
		t.Errorf("content changed unexpectedly to %q", patched.Content)
	}
	if patched.Pinned {
		t.Error("pinned changed unexpectedly to true")
	}
	if patched.AuthorID != 42 {
		t.Errorf("author_id changed unexpectedly to %d", patched.AuthorID)
	}
}

func TestAnnouncementServicePatchNotFound(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAnnouncementRepo()
	svc := NewAnnouncementService(repo)

	visible := true
	_, err := svc.Patch(ctx, bson.NewObjectID(), &AnnouncementPatch{Visible: &visible})
	if err != errs.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUserServicePatchOnlyChangesProvidedFields(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	uid := bson.NewObjectID()
	_ = repo.Create(ctx, &domain.User{
		ID:           uid,
		OnlineID:     42,
		Username:     "player42",
		Roles:        []domain.UserRole{domain.RolePlayer},
		IsBanned:     false,
		VerifyStatus: domain.Pending,
	})
	inv := &mockInvalidator{}
	revoker := &sessionRevokerStub{}
	svc := NewUserService(repo, inv, revoker)

	banned := true
	verified := domain.Verified
	patched, err := svc.Patch(ctx, uid, &UserPatch{Banned: &banned, VerifyStatus: &verified})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if !patched.IsBanned {
		t.Error("expected IsBanned to be true")
	}
	if patched.VerifyStatus != domain.Verified {
		t.Errorf("verify_status = %q, want verified", patched.VerifyStatus)
	}
	if len(patched.Roles) != 1 || patched.Roles[0] != domain.RolePlayer {
		t.Errorf("roles changed unexpectedly to %v", patched.Roles)
	}
	if len(inv.userCalls) != 1 || inv.userCalls[0] != 42 {
		t.Errorf("expected InvalidateUser(42), got %v", inv.userCalls)
	}
	if len(revoker.userIDs) != 1 || revoker.userIDs[0] != uid.Hex() {
		t.Errorf("expected session revoked for %s, got %v", uid.Hex(), revoker.userIDs)
	}
}

func TestUserServicePatchInvalidVerifyStatus(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	uid := bson.NewObjectID()
	_ = repo.Create(ctx, &domain.User{ID: uid, OnlineID: 42})
	inv := &mockInvalidator{}
	revoker := &sessionRevokerStub{}
	svc := NewUserService(repo, inv, revoker)

	badStatus := domain.VerifyStatus("invalid")
	_, err := svc.Patch(ctx, uid, &UserPatch{VerifyStatus: &badStatus})
	if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
	if len(inv.userCalls) != 0 {
		t.Errorf("expected no cache invalidation, got %v", inv.userCalls)
	}
	if len(revoker.userIDs) != 0 {
		t.Errorf("expected no session revocation, got %v", revoker.userIDs)
	}
}

func TestUserServicePatchNotFoundDoesNotInvalidate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	inv := &mockInvalidator{}
	svc := NewUserService(repo, inv, nil)

	banned := true
	_, err := svc.Patch(ctx, bson.NewObjectID(), &UserPatch{Banned: &banned})
	if err != errs.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if len(inv.userCalls) != 0 {
		t.Errorf("expected no invalidation on failure, got %v", inv.userCalls)
	}
}
