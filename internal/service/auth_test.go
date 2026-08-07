package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/oauth"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/jwtutil"
	"rctHubBackend/pkg/paginate"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// fakeOAuthClient is a stub OAuth client for auth service tests.
type fakeOAuthClient struct {
	authURL string
	token   *oauth2.Token
	user    *oauth.OsuUser
	err     error
}

func (f *fakeOAuthClient) AuthURL(ctx context.Context) (string, error) {
	return f.authURL, f.err
}

func (f *fakeOAuthClient) Exchange(ctx context.Context, code, state string) (*oauth2.Token, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.token, nil
}

func (f *fakeOAuthClient) Me(ctx context.Context, token *oauth2.Token) (*oauth.OsuUser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.user, nil
}

// fakeUserRepo is an in-memory user repository.
type fakeUserRepo struct {
	users map[bson.ObjectID]*domain.User
	byID  map[int64]*domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{
		users: make(map[bson.ObjectID]*domain.User),
		byID:  make(map[int64]*domain.User),
	}
}

func (r *fakeUserRepo) Create(ctx context.Context, user *domain.User) error {
	r.users[user.ID] = user
	r.byID[user.OnlineID] = user
	return nil
}

func (r *fakeUserRepo) Update(ctx context.Context, user *domain.User) error {
	if _, ok := r.users[user.ID]; !ok {
		return errs.ErrNotFound
	}
	r.users[user.ID] = user
	r.byID[user.OnlineID] = user
	return nil
}

func (r *fakeUserRepo) ByID(ctx context.Context, id bson.ObjectID) (*domain.User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return user, nil
}

func (r *fakeUserRepo) ByOsuID(ctx context.Context, osuID int64) (*domain.User, error) {
	user, ok := r.byID[osuID]
	if !ok {
		return nil, errs.ErrNotFound
	}
	return user, nil
}

func (r *fakeUserRepo) List(ctx context.Context, params paginate.Params) (paginate.Result[domain.User], error) {
	return paginate.Result[domain.User]{}, nil
}

func (r *fakeUserRepo) UpsertOsuFields(ctx context.Context, osuID int64, fields bson.M) (*domain.User, error) {
	user, ok := r.byID[osuID]
	if !ok {
		user = &domain.User{
			ID:           bson.NewObjectID(),
			OnlineID:     osuID,
			Roles:        []domain.UserRole{domain.RolePlayer},
			VerifyStatus: domain.Pending,
		}
		r.users[user.ID] = user
		r.byID[osuID] = user
	}
	if value, ok := fields["username"].(string); ok {
		user.Username = value
	}
	if value, ok := fields["avatar_url"].(string); ok {
		user.AvatarURL = value
	}
	if value, ok := fields["country_code"].(string); ok {
		user.CountryCode = value
	}
	return user, nil
}

var _ repository.UserRepository = (*fakeUserRepo)(nil)
var _ oauth.OAuthClient = (*fakeOAuthClient)(nil)

func countryCode(code string) struct {
	Code string `json:"code"`
} {
	return struct {
		Code string `json:"code"`
	}{Code: code}
}

func TestAuthServiceCallbackCreatesUser(t *testing.T) {
	ctx := context.Background()
	oauthClient := &fakeOAuthClient{
		authURL: "https://osu.ppy.sh/oauth/authorize?test",
		token:   &oauth2.Token{AccessToken: "test-token"},
		user: &oauth.OsuUser{
			ID:        42,
			Username:  "tester",
			AvatarURL: "https://a.ppy.sh/42",
			Country:   countryCode("CN"),
		},
	}
	users := newFakeUserRepo()
	signer := jwtutil.NewSigner("this-is-a-32-byte-secret-key-for-test!", "rcthub-test")
	svc := NewAuthService(oauthClient, users, nil, signer, time.Hour, nil)

	token, user, err := svc.Callback(ctx, "code", "state")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if token == "" {
		t.Error("expected jwt token")
	}
	if user == nil {
		t.Fatal("expected user")
	}
	if user.Username != "tester" {
		t.Errorf("expected username tester, got %s", user.Username)
	}
	if user.OnlineID != 42 {
		t.Errorf("expected online id 42, got %d", user.OnlineID)
	}
	if len(user.Roles) != 1 || user.Roles[0] != domain.RolePlayer {
		t.Errorf("expected role player, got %v", user.Roles)
	}
}

func TestAuthServiceCallbackUpdatesExistingUser(t *testing.T) {
	ctx := context.Background()
	existing := &domain.User{
		ID:           bson.NewObjectID(),
		OnlineID:     42,
		Username:     "oldname",
		Roles:        []domain.UserRole{domain.RolePlayer},
		VerifyStatus: domain.Verified,
	}
	users := newFakeUserRepo()
	_ = users.Create(ctx, existing)

	oauthClient := &fakeOAuthClient{
		token: &oauth2.Token{AccessToken: "test-token"},
		user: &oauth.OsuUser{
			ID:        42,
			Username:  "newname",
			AvatarURL: "https://a.ppy.sh/42",
			Country:   countryCode("JP"),
		},
	}
	signer := jwtutil.NewSigner("this-is-a-32-byte-secret-key-for-test!", "rcthub-test")
	inv := &mockInvalidator{}
	svc := NewAuthService(oauthClient, users, inv, signer, time.Hour, nil)

	_, user, err := svc.Callback(ctx, "code", "state")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if user.Username != "newname" {
		t.Errorf("expected updated username newname, got %s", user.Username)
	}
	if user.CountryCode != "JP" {
		t.Errorf("expected updated country JP, got %s", user.CountryCode)
	}
	if user.VerifyStatus != domain.Verified || len(user.Roles) != 1 || user.Roles[0] != domain.RolePlayer {
		t.Errorf("expected local authorization fields to be preserved, got status=%s roles=%v", user.VerifyStatus, user.Roles)
	}
	if len(inv.userCalls) != 1 || inv.userCalls[0] != 42 {
		t.Errorf("expected InvalidateUser(42), got %v", inv.userCalls)
	}
}

func TestAuthServiceCallbackRejectsBannedUser(t *testing.T) {
	ctx := context.Background()
	existing := &domain.User{
		ID:           bson.NewObjectID(),
		OnlineID:     42,
		Username:     "banned",
		Roles:        []domain.UserRole{domain.RolePlayer},
		IsBanned:     true,
		VerifyStatus: domain.Verified,
	}
	users := newFakeUserRepo()
	_ = users.Create(ctx, existing)

	oauthClient := &fakeOAuthClient{
		token: &oauth2.Token{AccessToken: "test-token"},
		user: &oauth.OsuUser{
			ID:      42,
			Country: countryCode("CN"),
		},
	}
	signer := jwtutil.NewSigner("this-is-a-32-byte-secret-key-for-test!", "rcthub-test")
	svc := NewAuthService(oauthClient, users, nil, signer, time.Hour, nil)

	_, _, err := svc.Callback(ctx, "code", "state")
	if err != errs.ErrForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestAuthServiceCallbackReturnsCacheInvalidationFailure(t *testing.T) {
	ctx := context.Background()
	users := newFakeUserRepo()
	oauthClient := &fakeOAuthClient{
		token: &oauth2.Token{AccessToken: "test-token"},
		user: &oauth.OsuUser{
			ID:       42,
			Username: "tester",
			Country:  countryCode("CN"),
		},
	}
	cacheErr := errors.New("redis unavailable")
	inv := &mockInvalidator{err: cacheErr}
	signer := jwtutil.NewSigner("this-is-a-32-byte-secret-key-for-test!", "rcthub-test")
	svc := NewAuthService(oauthClient, users, inv, signer, time.Hour, nil)

	token, user, err := svc.Callback(ctx, "code", "state")
	if !errors.Is(err, cacheErr) {
		t.Fatalf("expected cache invalidation error, got %v", err)
	}
	if token != "" || user != nil {
		t.Fatal("expected login to fail closed before issuing a token")
	}
}
