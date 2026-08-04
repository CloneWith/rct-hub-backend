package service

import (
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// mockInvalidator records cache invalidation calls for verification.
type mockInvalidator struct {
	userCalls    []int64
	beatmapCalls []int64
	err          error
}

func (m *mockInvalidator) InvalidateUser(_ context.Context, osuID int64) error {
	m.userCalls = append(m.userCalls, osuID)
	return m.err
}

func (m *mockInvalidator) InvalidateBeatmap(_ context.Context, osuID int64) error {
	m.beatmapCalls = append(m.beatmapCalls, osuID)
	return m.err
}

// --- UserService tests ---

func TestUserServiceUpdateRolesInvalidatesCache(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	uid := bson.NewObjectID()
	_ = repo.Create(ctx, &domain.User{
		ID:       uid,
		OnlineID: 42,
		Username: "player42",
		Roles:    []domain.UserRole{domain.RolePlayer},
	})
	inv := &mockInvalidator{}
	svc := NewUserService(repo, inv)

	_, err := svc.UpdateRoles(ctx, uid, []domain.UserRole{domain.RoleAdmin})
	if err != nil {
		t.Fatalf("UpdateRoles: %v", err)
	}

	if len(inv.userCalls) != 1 || inv.userCalls[0] != 42 {
		t.Errorf("expected InvalidateUser(42), got %v", inv.userCalls)
	}
}

func TestUserServiceSetBannedInvalidatesCache(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	uid := bson.NewObjectID()
	_ = repo.Create(ctx, &domain.User{
		ID:       uid,
		OnlineID: 99,
		Username: "player99",
		Roles:    []domain.UserRole{domain.RolePlayer},
	})
	inv := &mockInvalidator{}
	svc := NewUserService(repo, inv)

	_, err := svc.SetBanned(ctx, uid, true)
	if err != nil {
		t.Fatalf("SetBanned: %v", err)
	}

	if len(inv.userCalls) != 1 || inv.userCalls[0] != 99 {
		t.Errorf("expected InvalidateUser(99), got %v", inv.userCalls)
	}

	// Verify the ban was actually applied.
	user, _ := repo.ByID(ctx, uid)
	if !user.IsBanned {
		t.Error("expected user to be banned")
	}
}

func TestUserServiceSetVerifyStatusInvalidatesCache(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	uid := bson.NewObjectID()
	_ = repo.Create(ctx, &domain.User{
		ID:           uid,
		OnlineID:     77,
		Username:     "player77",
		Roles:        []domain.UserRole{domain.RolePlayer},
		VerifyStatus: domain.Pending,
	})
	inv := &mockInvalidator{}
	svc := NewUserService(repo, inv)

	_, err := svc.SetVerifyStatus(ctx, uid, domain.Verified)
	if err != nil {
		t.Fatalf("SetVerifyStatus: %v", err)
	}

	if len(inv.userCalls) != 1 || inv.userCalls[0] != 77 {
		t.Errorf("expected InvalidateUser(77), got %v", inv.userCalls)
	}
}

func TestUserServiceUpdateRolesNotFoundDoesNotInvalidate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	inv := &mockInvalidator{}
	svc := NewUserService(repo, inv)

	_, err := svc.UpdateRoles(ctx, bson.NewObjectID(), []domain.UserRole{domain.RoleAdmin})
	if err != errs.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if len(inv.userCalls) != 0 {
		t.Errorf("expected no invalidation on failure, got %v", inv.userCalls)
	}
}

func TestUserServiceNilInvalidatorIsSafe(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	uid := bson.NewObjectID()
	_ = repo.Create(ctx, &domain.User{
		ID:       uid,
		OnlineID: 1,
		Username: "p1",
		Roles:    []domain.UserRole{domain.RolePlayer},
	})
	svc := NewUserService(repo, nil) // nil invalidator — should not panic

	_, err := svc.UpdateRoles(ctx, uid, []domain.UserRole{domain.RoleAdmin})
	if err != nil {
		t.Fatalf("UpdateRoles with nil invalidator: %v", err)
	}
}

func TestUserServiceReturnsCacheInvalidationFailure(t *testing.T) {
	ctx := context.Background()
	repo := newFakeUserRepo()
	uid := bson.NewObjectID()
	_ = repo.Create(ctx, &domain.User{ID: uid, OnlineID: 42})
	cacheErr := errors.New("redis unavailable")
	svc := NewUserService(repo, &mockInvalidator{err: cacheErr})

	_, err := svc.SetBanned(ctx, uid, true)
	if !errors.Is(err, cacheErr) {
		t.Fatalf("expected cache invalidation error, got %v", err)
	}
}

// --- fakeBeatmapRepo ---

type fakeBeatmapRepo struct {
	byObjID map[bson.ObjectID]*domain.Beatmap
	byOsu   map[int64]*domain.Beatmap
}

func newFakeBeatmapRepo() *fakeBeatmapRepo {
	return &fakeBeatmapRepo{
		byObjID: make(map[bson.ObjectID]*domain.Beatmap),
		byOsu:   make(map[int64]*domain.Beatmap),
	}
}

func (r *fakeBeatmapRepo) Create(_ context.Context, bm *domain.Beatmap) error {
	r.byObjID[bm.ID] = bm
	r.byOsu[bm.OnlineID] = bm
	return nil
}

func (r *fakeBeatmapRepo) Update(_ context.Context, bm *domain.Beatmap) error {
	if _, ok := r.byObjID[bm.ID]; !ok {
		return errs.ErrNotFound
	}
	r.byObjID[bm.ID] = bm
	r.byOsu[bm.OnlineID] = bm
	return nil
}

func (r *fakeBeatmapRepo) ByID(_ context.Context, id bson.ObjectID) (*domain.Beatmap, error) {
	bm, ok := r.byObjID[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return bm, nil
}

func (r *fakeBeatmapRepo) ByOsuID(_ context.Context, osuID int64) (*domain.Beatmap, error) {
	bm, ok := r.byOsu[osuID]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return bm, nil
}

func (r *fakeBeatmapRepo) List(_ context.Context, params paginate.Params) (paginate.Result[domain.Beatmap], error) {
	return paginate.Result[domain.Beatmap]{}, nil
}

func (r *fakeBeatmapRepo) Delete(_ context.Context, id bson.ObjectID) error {
	bm, ok := r.byObjID[id]
	if !ok {
		return errs.ErrNotFound
	}
	delete(r.byObjID, id)
	delete(r.byOsu, bm.OnlineID)
	return nil
}

func (r *fakeBeatmapRepo) UpsertOsuFields(_ context.Context, osuID int64, fields bson.M) (*domain.Beatmap, error) {
	return nil, errs.ErrNotFound
}

// --- BeatmapService tests ---

func TestBeatmapServiceUpdateInvalidatesCache(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	bid := bson.NewObjectID()
	bm := &domain.Beatmap{
		ID:        bid,
		OnlineID:  100,
		Title:     "Old Title",
		ModString: "",
	}
	_ = repo.Create(ctx, bm)

	inv := &mockInvalidator{}
	svc := NewBeatmapService(repo, inv)

	bm.ModString = "HD"
	err := svc.Update(ctx, bm)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if len(inv.beatmapCalls) != 1 || inv.beatmapCalls[0] != 100 {
		t.Errorf("expected InvalidateBeatmap(100), got %v", inv.beatmapCalls)
	}
}

func TestBeatmapServiceUpdateRejectsOnlineIDChange(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	bid := bson.NewObjectID()
	stored := &domain.Beatmap{ID: bid, OnlineID: 100, Title: "Original"}
	_ = repo.Create(ctx, stored)
	svc := NewBeatmapService(repo, &mockInvalidator{})

	changed := *stored
	changed.OnlineID = 101
	err := svc.Update(ctx, &changed)
	if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
	if got, _ := repo.ByID(ctx, bid); got.OnlineID != 100 {
		t.Fatalf("expected stored osu! id to remain 100, got %d", got.OnlineID)
	}
}

func TestBeatmapServiceReturnsCacheInvalidationFailure(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	bid := bson.NewObjectID()
	stored := &domain.Beatmap{ID: bid, OnlineID: 100}
	_ = repo.Create(ctx, stored)
	cacheErr := errors.New("redis unavailable")
	svc := NewBeatmapService(repo, &mockInvalidator{err: cacheErr})

	err := svc.Update(ctx, stored)
	if !errors.Is(err, cacheErr) {
		t.Fatalf("expected cache invalidation error, got %v", err)
	}
}

func TestBeatmapServiceDeleteInvalidatesCache(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	bid := bson.NewObjectID()
	_ = repo.Create(ctx, &domain.Beatmap{
		ID:       bid,
		OnlineID: 200,
		Title:    "To Delete",
	})

	inv := &mockInvalidator{}
	svc := NewBeatmapService(repo, inv)

	err := svc.Delete(ctx, bid)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if len(inv.beatmapCalls) != 1 || inv.beatmapCalls[0] != 200 {
		t.Errorf("expected InvalidateBeatmap(200), got %v", inv.beatmapCalls)
	}

	// Verify it was actually deleted.
	_, err = repo.ByID(ctx, bid)
	if err != errs.ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestBeatmapServiceDeleteNotFoundDoesNotInvalidate(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	inv := &mockInvalidator{}
	svc := NewBeatmapService(repo, inv)

	err := svc.Delete(ctx, bson.NewObjectID())
	if err != errs.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if len(inv.beatmapCalls) != 0 {
		t.Errorf("expected no invalidation on failure, got %v", inv.beatmapCalls)
	}
}

func TestBeatmapServiceCreateInvalidatesCache(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	inv := &mockInvalidator{}
	svc := NewBeatmapService(repo, inv)

	bm := &domain.Beatmap{
		ID:       bson.NewObjectID(),
		OnlineID: 300,
		Title:    "New Map",
	}
	err := svc.Create(ctx, bm)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(inv.beatmapCalls) != 1 || inv.beatmapCalls[0] != 300 {
		t.Errorf("expected InvalidateBeatmap(300), got %v", inv.beatmapCalls)
	}
}

func TestBeatmapServiceNilInvalidatorIsSafe(t *testing.T) {
	ctx := context.Background()
	repo := newFakeBeatmapRepo()
	svc := NewBeatmapService(repo, nil) // nil invalidator — should not panic

	bm := &domain.Beatmap{
		ID:       bson.NewObjectID(),
		OnlineID: 500,
		Title:    "Safe Map",
	}
	if err := svc.Create(ctx, bm); err != nil {
		t.Fatalf("Create with nil invalidator: %v", err)
	}
	if err := svc.Update(ctx, bm); err != nil {
		t.Fatalf("Update with nil invalidator: %v", err)
	}
	if err := svc.Delete(ctx, bm.ID); err != nil {
		t.Fatalf("Delete with nil invalidator: %v", err)
	}
}
