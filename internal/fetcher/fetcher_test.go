package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// ============================================================================
// Fake repositories
// ============================================================================

// fakeUserRepo is an in-memory UserRepository.
type fakeUserRepo struct {
	byObjID map[bson.ObjectID]*domain.User
	byOsu   map[int64]*domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{
		byObjID: make(map[bson.ObjectID]*domain.User),
		byOsu:   make(map[int64]*domain.User),
	}
}

func (r *fakeUserRepo) Create(_ context.Context, user *domain.User) error {
	if _, ok := r.byOsu[user.OnlineID]; ok {
		return errs.ErrAlreadyExists
	}
	user.ID = bson.NewObjectID()
	r.byObjID[user.ID] = user
	r.byOsu[user.OnlineID] = user
	return nil
}

func (r *fakeUserRepo) Update(_ context.Context, user *domain.User) error {
	if _, ok := r.byObjID[user.ID]; !ok {
		return errs.ErrNotFound
	}
	r.byObjID[user.ID] = user
	r.byOsu[user.OnlineID] = user
	return nil
}

func (r *fakeUserRepo) UpsertOsuFields(_ context.Context, osuID int64, fields bson.M) (*domain.User, error) {
	existing, ok := r.byOsu[osuID]
	if !ok {
		existing = &domain.User{
			ID:           bson.NewObjectID(),
			OnlineID:     osuID,
			Roles:        []domain.UserRole{domain.RolePlayer},
			VerifyStatus: domain.Pending,
		}
		r.byObjID[existing.ID] = existing
		r.byOsu[osuID] = existing
	}
	if v, ok := fields["username"]; ok {
		existing.Username = v.(string)
	}
	if v, ok := fields["avatar_url"]; ok {
		existing.AvatarURL = v.(string)
	}
	if v, ok := fields["country_code"]; ok {
		existing.CountryCode = v.(string)
	}
	if v, ok := fields["global_rank"]; ok {
		existing.GlobalRank = v.(int64)
	}
	if v, ok := fields["pp"]; ok {
		existing.PP = v.(float32)
	}
	return existing, nil
}

func (r *fakeUserRepo) ByID(_ context.Context, id bson.ObjectID) (*domain.User, error) {
	u, ok := r.byObjID[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return u, nil
}

func (r *fakeUserRepo) ByOsuID(_ context.Context, osuID int64) (*domain.User, error) {
	u, ok := r.byOsu[osuID]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return u, nil
}

func (r *fakeUserRepo) List(_ context.Context, _ paginate.Params) (paginate.Result[domain.User], error) {
	return paginate.Result[domain.User]{}, nil
}

// fakeBeatmapRepo is an in-memory BeatmapRepository.
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
	if _, ok := r.byOsu[bm.OnlineID]; ok {
		return errs.ErrAlreadyExists
	}
	bm.ID = bson.NewObjectID()
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

func (r *fakeBeatmapRepo) UpsertOsuFields(_ context.Context, osuID int64, fields bson.M) (*domain.Beatmap, error) {
	existing, ok := r.byOsu[osuID]
	if !ok {
		existing = &domain.Beatmap{
			ID:       bson.NewObjectID(),
			OnlineID: osuID,
		}
		r.byObjID[existing.ID] = existing
		r.byOsu[osuID] = existing
	}
	if v, ok := fields["beatmapset_id"]; ok {
		existing.BeatmapsetID = v.(int64)
	}
	if v, ok := fields["title"]; ok {
		existing.Title = v.(string)
	}
	if v, ok := fields["artist"]; ok {
		existing.Artist = v.(string)
	}
	if v, ok := fields["version"]; ok {
		existing.DifficultyName = v.(string)
	}
	if v, ok := fields["user_id"]; ok {
		existing.AuthorID = v.(int64)
	}
	if v, ok := fields["mode_int"]; ok {
		existing.RulesetID = v.(int)
	}
	if v, ok := fields["status"]; ok {
		existing.Status = v.(string)
	}
	if v, ok := fields["difficulty_rating"]; ok {
		existing.StarRating = v.(float64)
	}
	if v, ok := fields["bpm"]; ok {
		existing.BPM = v.(float64)
	}
	if v, ok := fields["total_length"]; ok {
		existing.TotalLength = v.(int)
	}
	if v, ok := fields["drain"]; ok {
		existing.DrainRate = v.(float64)
	}
	if v, ok := fields["cs"]; ok {
		existing.CircleSize = v.(float64)
	}
	if v, ok := fields["ar"]; ok {
		existing.ApproachRate = v.(float64)
	}
	if v, ok := fields["accuracy"]; ok {
		existing.OverallDifficulty = v.(float64)
	}
	if v, ok := fields["cover_url"]; ok {
		existing.CoverURL = v.(string)
	}
	return existing, nil
}

func (r *fakeBeatmapRepo) ByID(_ context.Context, id bson.ObjectID) (*domain.Beatmap, error) {
	b, ok := r.byObjID[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return b, nil
}

func (r *fakeBeatmapRepo) ByOsuID(_ context.Context, osuID int64) (*domain.Beatmap, error) {
	b, ok := r.byOsu[osuID]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return b, nil
}

func (r *fakeBeatmapRepo) List(_ context.Context, _ paginate.Params) (paginate.Result[domain.Beatmap], error) {
	return paginate.Result[domain.Beatmap]{}, nil
}

func (r *fakeBeatmapRepo) Delete(_ context.Context, id bson.ObjectID) error {
	delete(r.byObjID, id)
	return nil
}

// ============================================================================
// Test helpers
// ============================================================================

func testLogger() *zap.Logger {
	l, _ := zap.NewDevelopment()
	return l
}

func newMiniRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return rdb, mr
}

// osuTestServer starts a mock osu! API server that serves a fixed token
// and configurable user/beatmap responses.
type osuTestServer struct {
	*httptest.Server
	token       string
	userResp    map[int64]*OsuUserResponse
	beatmapResp map[int64]*OsuBeatmapResponse
}

func newOsuTestServer(t *testing.T) *osuTestServer {
	srv := &osuTestServer{
		token:       "test-access-token",
		userResp:    make(map[int64]*OsuUserResponse),
		beatmapResp: make(map[int64]*OsuBeatmapResponse),
	}
	srv.Server = httptest.NewServer(http.HandlerFunc(srv.handle))
	t.Cleanup(srv.Server.Close)
	return srv
}

func (s *osuTestServer) handle(w http.ResponseWriter, r *http.Request) {
	// Token endpoint.
	if r.URL.Path == "/oauth/token" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken: s.token,
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		})
		return
	}

	// All other endpoints require auth.
	auth := r.Header.Get("Authorization")
	if auth != "Bearer "+s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// User endpoint: /api/v2/users/{id}/osu
	if strings.HasPrefix(r.URL.Path, "/api/v2/users/") {
		path := strings.TrimPrefix(r.URL.Path, "/api/v2/users/")
		if !strings.HasSuffix(path, "/osu") {
			http.Error(w, "missing osu ruleset path", http.StatusBadRequest)
			return
		}
		var id int64
		fmt.Sscanf(strings.TrimSuffix(path, "/osu"), "%d", &id)
		if u, ok := s.userResp[id]; ok {
			json.NewEncoder(w).Encode(u)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Beatmap endpoint: /api/v2/beatmaps/{id}
	if strings.HasPrefix(r.URL.Path, "/api/v2/beatmaps/") {
		var id int64
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/api/v2/beatmaps/"), "%d", &id)
		if b, ok := s.beatmapResp[id]; ok {
			json.NewEncoder(w).Encode(b)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (s *osuTestServer) clientConfig() APIClientConfig {
	return APIClientConfig{
		ClientID:     "test-id",
		ClientSecret: "test-secret",
		APIBase:      s.Server.URL,
	}
}

func sampleUserResp(id int64) *OsuUserResponse {
	u := &OsuUserResponse{
		ID:        id,
		Username:  fmt.Sprintf("player%d", id),
		AvatarURL: fmt.Sprintf("https://a.ppy.sh/%d", id),
	}
	u.Country.Code = "CN"
	u.Statistics.GlobalRank = 1000 + id
	u.Statistics.PP = 4500.5
	return u
}

func sampleBeatmapResp(id int64) *OsuBeatmapResponse {
	b := &OsuBeatmapResponse{
		ID:               id,
		BeatmapsetID:     id * 10,
		Status:           "ranked",
		ModeInt:          0,
		DifficultyRating: 5.25,
		Version:          "Insane",
		TotalLength:      120,
		UserID:           123,
		BPM:              180,
		CS:               4,
		AR:               9,
		Drain:            5,
		Accuracy:         8,
	}
	b.Beatmapset.ID = id * 10
	b.Beatmapset.Title = "Test Song"
	b.Beatmapset.Artist = "Test Artist"
	b.Beatmapset.Covers.Cover = fmt.Sprintf("https://assets.ppy.sh/beatmaps/%d/covers/cover.jpg", id*10)
	return b
}

// ============================================================================
// Field mapping tests
// ============================================================================

func TestUserOsuFields(t *testing.T) {
	resp := sampleUserResp(42)
	fields := userOsuFields(resp)

	if fields["username"] != "player42" {
		t.Errorf("expected username player42, got %v", fields["username"])
	}
	if fields["avatar_url"] != "https://a.ppy.sh/42" {
		t.Errorf("expected avatar url, got %v", fields["avatar_url"])
	}
	if fields["country_code"] != "CN" {
		t.Errorf("expected country CN, got %v", fields["country_code"])
	}
	if fields["global_rank"] != int64(1042) {
		t.Errorf("expected global rank 1042, got %v", fields["global_rank"])
	}
	if fields["pp"] != float32(4500.5) {
		t.Errorf("expected pp 4500.5, got %v", fields["pp"])
	}
	// Verify only API-owned fields are present — no local-only fields.
	for _, key := range []string{"roles", "verify_status", "is_banned", "_id", "created_at"} {
		if _, ok := fields[key]; ok {
			t.Errorf("expected no local field %q in osu fields", key)
		}
	}
}

func TestBeatmapOsuFields(t *testing.T) {
	resp := sampleBeatmapResp(100)
	fields := beatmapOsuFields(resp)

	if fields["title"] != "Test Song" {
		t.Errorf("expected title Test Song, got %v", fields["title"])
	}
	if fields["artist"] != "Test Artist" {
		t.Errorf("expected artist Test Artist, got %v", fields["artist"])
	}
	if fields["version"] != "Insane" {
		t.Errorf("expected version Insane, got %v", fields["version"])
	}
	if fields["difficulty_rating"] != 5.25 {
		t.Errorf("expected star rating 5.25, got %v", fields["difficulty_rating"])
	}
	if fields["bpm"] != 180.0 {
		t.Errorf("expected bpm 180, got %v", fields["bpm"])
	}
	// Verify only API-owned fields are present — no local-only fields.
	for _, key := range []string{"mod_string", "mod_index", "skill", "comment", "_id", "created_at"} {
		if _, ok := fields[key]; ok {
			t.Errorf("expected no local field %q in osu fields", key)
		}
	}
}

// ============================================================================
// API client tests
// ============================================================================

func TestAPIClientGetUser(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.userResp[42] = sampleUserResp(42)

	rdb, _ := newMiniRedis(t)
	client := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	user, err := client.GetUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.ID != 42 {
		t.Errorf("expected id 42, got %d", user.ID)
	}
	if user.Username != "player42" {
		t.Errorf("expected username player42, got %s", user.Username)
	}
	if user.Statistics.GlobalRank != 1042 {
		t.Errorf("expected global rank 1042, got %d", user.Statistics.GlobalRank)
	}
}

func TestAPIClientGetUserNotFound(t *testing.T) {
	srv := newOsuTestServer(t)
	rdb, _ := newMiniRedis(t)
	client := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	_, err := client.GetUser(context.Background(), 999)
	if !isErrNotFound(err) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAPIClientGetBeatmap(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.beatmapResp[100] = sampleBeatmapResp(100)

	rdb, _ := newMiniRedis(t)
	client := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	bm, err := client.GetBeatmap(context.Background(), 100)
	if err != nil {
		t.Fatalf("GetBeatmap: %v", err)
	}
	if bm.ID != 100 {
		t.Errorf("expected id 100, got %d", bm.ID)
	}
	if bm.Beatmapset.Title != "Test Song" {
		t.Errorf("expected title Test Song, got %s", bm.Beatmapset.Title)
	}
	if bm.DifficultyRating != 5.25 {
		t.Errorf("expected difficulty rating 5.25, got %f", bm.DifficultyRating)
	}
}

func TestAPIClientTokenReuse(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.userResp[1] = sampleUserResp(1)
	srv.userResp[2] = sampleUserResp(2)

	rdb, _ := newMiniRedis(t)
	client := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	// First call acquires token + fetches user.
	_, err := client.GetUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("first GetUser: %v", err)
	}

	// Second call should reuse the cached token.
	_, err = client.GetUser(context.Background(), 2)
	if err != nil {
		t.Fatalf("second GetUser: %v", err)
	}

	// Verify token was cached in Redis.
	tok, err := rdb.Get(context.Background(), tokenCacheKey).Result()
	if err != nil {
		t.Fatalf("expected token in redis: %v", err)
	}
	if tok != "test-access-token" {
		t.Errorf("expected cached token, got %s", tok)
	}
}

// ============================================================================
// Fetcher integration tests
// ============================================================================

func TestFetcherGetUserFromDB(t *testing.T) {
	srv := newOsuTestServer(t)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	userRepo := newFakeUserRepo()
	// Pre-populate DB.
	existing := &domain.User{
		OnlineID:   42,
		Username:   "dbuser",
		AvatarURL:  "https://a.ppy.sh/42",
		GlobalRank: 500,
		PP:         3000,
		Roles:      []domain.UserRole{domain.RolePlayer},
	}
	_ = userRepo.Create(context.Background(), existing)

	f := New(apiClient, userRepo, newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	// GetUser should find it in DB without hitting the API.
	user, err := f.GetUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Username != "dbuser" {
		t.Errorf("expected dbuser from DB, got %s", user.Username)
	}

	// Verify the API was NOT called (no response configured for user 42
	// in the server, so if it was called it would 404).
}

func TestFetcherGetUserFallbackToAPI(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.userResp[42] = sampleUserResp(42)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	userRepo := newFakeUserRepo()
	f := New(apiClient, userRepo, newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	// DB miss → API fallback → persist.
	user, err := f.GetUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Username != "player42" {
		t.Errorf("expected player42 from API, got %s", user.Username)
	}

	// Verify it was persisted to DB.
	dbUser, _ := userRepo.ByOsuID(context.Background(), 42)
	if dbUser == nil {
		t.Fatal("expected user to be persisted in DB")
	}
	if dbUser.Username != "player42" {
		t.Errorf("expected persisted username player42, got %s", dbUser.Username)
	}

	// Verify it was cached in Redis.
	cached, err := rdb.Get(context.Background(), userCacheKey(42)).Bytes()
	if err != nil {
		t.Fatalf("expected user in redis cache: %v", err)
	}
	var cachedUser domain.User
	if err := json.Unmarshal(cached, &cachedUser); err != nil {
		t.Fatalf("unmarshal cached user: %v", err)
	}
	if cachedUser.Username != "player42" {
		t.Errorf("expected cached username player42, got %s", cachedUser.Username)
	}
}

func TestFetcherGetUserFromRedisCache(t *testing.T) {
	srv := newOsuTestServer(t)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	userRepo := newFakeUserRepo()
	f := New(apiClient, userRepo, newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	// Pre-populate Redis cache.
	cachedUser := &domain.User{
		OnlineID: 42,
		Username: "cacheduser",
	}
	data, _ := json.Marshal(cachedUser)
	rdb.Set(context.Background(), userCacheKey(42), data, time.Minute)

	// GetUser should return from Redis without hitting DB or API.
	user, err := f.GetUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Username != "cacheduser" {
		t.Errorf("expected cacheduser from Redis, got %s", user.Username)
	}
}

func TestFetcherSyncUserUpdatesExisting(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.userResp[42] = sampleUserResp(42)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	userRepo := newFakeUserRepo()
	existing := &domain.User{
		OnlineID:   42,
		Username:   "oldname",
		GlobalRank: 1,
		Roles:      []domain.UserRole{domain.RoleAdmin},
	}
	_ = userRepo.Create(context.Background(), existing)

	f := New(apiClient, userRepo, newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	user, err := f.SyncUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("SyncUser: %v", err)
	}
	if user.Username != "player42" {
		t.Errorf("expected updated username player42, got %s", user.Username)
	}
	if user.GlobalRank != 1042 {
		t.Errorf("expected updated global rank 1042, got %d", user.GlobalRank)
	}
	// Local field preserved.
	if len(user.Roles) != 1 || user.Roles[0] != domain.RoleAdmin {
		t.Errorf("expected preserved admin role, got %v", user.Roles)
	}
}

func TestFetcherSyncUserNotFound(t *testing.T) {
	srv := newOsuTestServer(t)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	f := New(apiClient, newFakeUserRepo(), newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	_, err := f.SyncUser(context.Background(), 999)
	if !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFetcherGetBeatmapFallbackToAPI(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.beatmapResp[100] = sampleBeatmapResp(100)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	bmRepo := newFakeBeatmapRepo()
	f := New(apiClient, newFakeUserRepo(), bmRepo, rdb, testLogger(), Config{})

	bm, err := f.GetBeatmap(context.Background(), 100)
	if err != nil {
		t.Fatalf("GetBeatmap: %v", err)
	}
	if bm.Title != "Test Song" {
		t.Errorf("expected Test Song, got %s", bm.Title)
	}
	if bm.StarRating != 5.25 {
		t.Errorf("expected 5.25, got %f", bm.StarRating)
	}

	// Verify persisted.
	dbBm, _ := bmRepo.ByOsuID(context.Background(), 100)
	if dbBm == nil {
		t.Fatal("expected beatmap in DB")
	}
}

func TestFetcherSyncBeatmapPreservesLocalFields(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.beatmapResp[100] = sampleBeatmapResp(100)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	bmRepo := newFakeBeatmapRepo()
	existing := &domain.Beatmap{
		OnlineID:  100,
		Title:     "Old Title",
		ModString: "HD",
		ModIndex:  2,
		Skill:     "alt",
	}
	_ = bmRepo.Create(context.Background(), existing)

	f := New(apiClient, newFakeUserRepo(), bmRepo, rdb, testLogger(), Config{})

	bm, err := f.SyncBeatmap(context.Background(), 100)
	if err != nil {
		t.Fatalf("SyncBeatmap: %v", err)
	}
	if bm.Title != "Test Song" {
		t.Errorf("expected updated title, got %s", bm.Title)
	}
	if bm.ModString != "HD" {
		t.Errorf("expected preserved mod_string, got %s", bm.ModString)
	}
	if bm.ModIndex != 2 {
		t.Errorf("expected preserved mod_index, got %d", bm.ModIndex)
	}
	if bm.Skill != "alt" {
		t.Errorf("expected preserved skill, got %s", bm.Skill)
	}
}

func TestFetcherGetBeatmapFromRedisCache(t *testing.T) {
	srv := newOsuTestServer(t)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	f := New(apiClient, newFakeUserRepo(), newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	cachedBm := &domain.Beatmap{
		OnlineID: 100,
		Title:    "Cached Map",
	}
	data, _ := json.Marshal(cachedBm)
	rdb.Set(context.Background(), beatmapCacheKey(100), data, time.Minute)

	bm, err := f.GetBeatmap(context.Background(), 100)
	if err != nil {
		t.Fatalf("GetBeatmap: %v", err)
	}
	if bm.Title != "Cached Map" {
		t.Errorf("expected Cached Map from Redis, got %s", bm.Title)
	}
}

// ============================================================================
// Review fix tests (Issues 1-4)
// ============================================================================

// TestFetcherSyncUserAssignsObjectID verifies that a cold API miss produces
// a user with a non-zero MongoDB ObjectID (Issue 2).
func TestFetcherSyncUserAssignsObjectID(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.userResp[42] = sampleUserResp(42)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	userRepo := newFakeUserRepo()
	f := New(apiClient, userRepo, newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	user, err := f.SyncUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("SyncUser: %v", err)
	}
	if user.ID.IsZero() {
		t.Fatal("expected non-zero ObjectID after API miss")
	}
	if user.OnlineID != 42 {
		t.Errorf("expected OnlineID 42, got %d", user.OnlineID)
	}
}

// TestFetcherSyncUserPreservesConcurrentLocalFields verifies that UpsertOsuFields
// only touches API-owned fields, leaving local-only fields (Roles, VerifyStatus,
// IsBanned) untouched — the core fix for Issue 1.
func TestFetcherSyncUserPreservesConcurrentLocalFields(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.userResp[42] = sampleUserResp(42)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	userRepo := newFakeUserRepo()
	// Pre-create a user with local-only fields set by an admin.
	existing := &domain.User{
		ID:           bson.NewObjectID(),
		OnlineID:     42,
		Username:     "oldname",
		Roles:        []domain.UserRole{domain.RoleAdmin},
		VerifyStatus: domain.Verified,
		IsBanned:     true,
	}
	_ = userRepo.Create(context.Background(), existing)

	f := New(apiClient, userRepo, newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	user, err := f.SyncUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("SyncUser: %v", err)
	}
	// API fields updated.
	if user.Username != "player42" {
		t.Errorf("expected updated username, got %s", user.Username)
	}
	if user.GlobalRank != 1042 {
		t.Errorf("expected updated global rank, got %d", user.GlobalRank)
	}
	// Local fields preserved — not overwritten by the sync.
	if len(user.Roles) != 1 || user.Roles[0] != domain.RoleAdmin {
		t.Errorf("expected preserved admin role, got %v", user.Roles)
	}
	if user.VerifyStatus != domain.Verified {
		t.Errorf("expected preserved verify status, got %s", user.VerifyStatus)
	}
	if !user.IsBanned {
		t.Error("expected preserved is_banned=true")
	}
}

// TestFetcherSyncUserDuplicateCreateRecovery verifies that two concurrent
// SyncUser calls for the same cold miss both succeed without error (Issue 3).
// With the atomic upsert, the "loser" simply updates the same document.
func TestFetcherSyncUserDuplicateCreateRecovery(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.userResp[42] = sampleUserResp(42)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	userRepo := newFakeUserRepo()
	f := New(apiClient, userRepo, newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	// First call creates the user.
	user1, err := f.SyncUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("first SyncUser: %v", err)
	}
	// Second call should succeed (upsert, not duplicate-create error).
	user2, err := f.SyncUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("second SyncUser: %v", err)
	}
	// Both should return the same ObjectID.
	if user1.ID != user2.ID {
		t.Errorf("expected same ObjectID, got %s and %s", user1.ID, user2.ID)
	}
	if user2.ID.IsZero() {
		t.Error("expected non-zero ObjectID")
	}
}

// TestFetcherSyncBeatmapAssignsObjectID verifies non-zero ObjectID on beatmap
// API miss (Issue 2).
func TestFetcherSyncBeatmapAssignsObjectID(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.beatmapResp[100] = sampleBeatmapResp(100)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	bmRepo := newFakeBeatmapRepo()
	f := New(apiClient, newFakeUserRepo(), bmRepo, rdb, testLogger(), Config{})

	bm, err := f.SyncBeatmap(context.Background(), 100)
	if err != nil {
		t.Fatalf("SyncBeatmap: %v", err)
	}
	if bm.ID.IsZero() {
		t.Fatal("expected non-zero ObjectID after API miss")
	}
}

// TestFetcherInvalidateUser verifies that InvalidateUser removes the cached
// user from Redis (Issue 4).
func TestFetcherInvalidateUser(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.userResp[42] = sampleUserResp(42)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	userRepo := newFakeUserRepo()
	f := New(apiClient, userRepo, newFakeBeatmapRepo(), rdb, testLogger(), Config{})

	// Populate cache via GetUser (API fallback).
	_, err := f.GetUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	// Verify cache entry exists.
	exists, _ := rdb.Exists(context.Background(), userCacheKey(42)).Result()
	if exists != 1 {
		t.Fatal("expected user in cache before invalidation")
	}

	// Invalidate.
	if err := f.InvalidateUser(context.Background(), 42); err != nil {
		t.Fatalf("InvalidateUser: %v", err)
	}

	// Verify cache entry is gone.
	exists, _ = rdb.Exists(context.Background(), userCacheKey(42)).Result()
	if exists != 0 {
		t.Fatal("expected cache entry to be removed after invalidation")
	}
}

// TestFetcherInvalidateBeatmap verifies cache invalidation for beatmaps (Issue 4).
func TestFetcherInvalidateBeatmap(t *testing.T) {
	srv := newOsuTestServer(t)
	srv.beatmapResp[100] = sampleBeatmapResp(100)
	rdb, _ := newMiniRedis(t)
	apiClient := NewAPIClient(srv.clientConfig(), rdb, testLogger())

	bmRepo := newFakeBeatmapRepo()
	f := New(apiClient, newFakeUserRepo(), bmRepo, rdb, testLogger(), Config{})

	_, err := f.GetBeatmap(context.Background(), 100)
	if err != nil {
		t.Fatalf("GetBeatmap: %v", err)
	}

	exists, _ := rdb.Exists(context.Background(), beatmapCacheKey(100)).Result()
	if exists != 1 {
		t.Fatal("expected beatmap in cache before invalidation")
	}

	if err := f.InvalidateBeatmap(context.Background(), 100); err != nil {
		t.Fatalf("InvalidateBeatmap: %v", err)
	}

	exists, _ = rdb.Exists(context.Background(), beatmapCacheKey(100)).Result()
	if exists != 0 {
		t.Fatal("expected cache entry to be removed after invalidation")
	}
}

func TestStaleUserReadCannotRefillCacheAfterInvalidation(t *testing.T) {
	ctx := context.Background()
	rdb, _ := newMiniRedis(t)
	f := New(nil, newFakeUserRepo(), newFakeBeatmapRepo(), rdb, testLogger(), Config{}).(*fetcher)
	staleGeneration := f.cacheGeneration(ctx, userGenerationKey(42))

	if err := f.InvalidateUser(ctx, 42); err != nil {
		t.Fatalf("InvalidateUser: %v", err)
	}
	f.cacheUser(ctx, &domain.User{OnlineID: 42, IsBanned: false}, staleGeneration)

	if exists, _ := rdb.Exists(ctx, userCacheKey(42)).Result(); exists != 0 {
		t.Fatal("stale user was written after cache invalidation")
	}

	currentGeneration := f.cacheGeneration(ctx, userGenerationKey(42))
	f.cacheUser(ctx, &domain.User{OnlineID: 42, IsBanned: true}, currentGeneration)
	user, ok := f.getCachedUser(ctx, 42)
	if !ok || !user.IsBanned {
		t.Fatal("current user generation was not cached")
	}
}

func TestStaleBeatmapReadCannotRefillCacheAfterInvalidation(t *testing.T) {
	ctx := context.Background()
	rdb, _ := newMiniRedis(t)
	f := New(nil, newFakeUserRepo(), newFakeBeatmapRepo(), rdb, testLogger(), Config{}).(*fetcher)
	staleGeneration := f.cacheGeneration(ctx, beatmapGenerationKey(100))

	if err := f.InvalidateBeatmap(ctx, 100); err != nil {
		t.Fatalf("InvalidateBeatmap: %v", err)
	}
	f.cacheBeatmap(ctx, &domain.Beatmap{OnlineID: 100, Title: "stale"}, staleGeneration)

	if exists, _ := rdb.Exists(ctx, beatmapCacheKey(100)).Result(); exists != 0 {
		t.Fatal("stale beatmap was written after cache invalidation")
	}

	currentGeneration := f.cacheGeneration(ctx, beatmapGenerationKey(100))
	f.cacheBeatmap(ctx, &domain.Beatmap{OnlineID: 100, Title: "current"}, currentGeneration)
	bm, ok := f.getCachedBeatmap(ctx, 100)
	if !ok || bm.Title != "current" {
		t.Fatal("current beatmap generation was not cached")
	}
}

// ============================================================================
// Helper
// ============================================================================

func isErrNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
