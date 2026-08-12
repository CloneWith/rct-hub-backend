package authsession

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
)

func TestStoreCreatesOpaqueSessionAndResolvesClaims(t *testing.T) {
	store, client, _ := newTestStore(t)
	user := &domain.User{ID: bson.NewObjectID(), OnlineID: 1001, Username: "captain", Roles: []domain.UserRole{domain.RolePlayer}}

	secret, err := store.Create(context.Background(), user)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if secret == "" {
		t.Fatal("Create returned an empty secret")
	}
	keys, err := client.Keys(context.Background(), sessionKeyPrefix+"*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("session keys = %v, err = %v", keys, err)
	}
	if keys[0] == sessionKeyPrefix+secret {
		t.Fatal("raw browser session secret was used as a Redis key")
	}

	claims, err := store.Resolve(context.Background(), secret)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if claims.UserID != user.ID.Hex() || claims.OsuID != user.OnlineID || claims.Username != user.Username || len(claims.Roles) != 1 {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestStoreEnforcesIdleAndAbsoluteExpiry(t *testing.T) {
	tests := []struct {
		name    string
		advance time.Duration
	}{
		{name: "idle", advance: 25 * time.Hour},
		{name: "absolute", advance: 8 * 24 * time.Hour},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _, now := newTestStore(t)
			user := &domain.User{ID: bson.NewObjectID(), OnlineID: 1001, Username: "captain"}
			secret, err := store.Create(context.Background(), user)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			*now = (*now).Add(test.advance)
			if _, err := store.Resolve(context.Background(), secret); !errors.Is(err, redis.Nil) {
				t.Fatalf("Resolve error = %v, want redis.Nil", err)
			}
		})
	}
}

func TestStoreRevokesOneOrEveryUserSession(t *testing.T) {
	store, _, _ := newTestStore(t)
	user := &domain.User{ID: bson.NewObjectID(), OnlineID: 1001, Username: "captain"}
	first, _ := store.Create(context.Background(), user)
	second, _ := store.Create(context.Background(), user)

	if err := store.Revoke(context.Background(), first); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := store.Resolve(context.Background(), first); !errors.Is(err, redis.Nil) {
		t.Fatalf("revoked session error = %v", err)
	}
	if _, err := store.Resolve(context.Background(), second); err != nil {
		t.Fatalf("unrelated session was revoked: %v", err)
	}
	if err := store.RevokeUser(context.Background(), user.ID.Hex()); err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}
	if _, err := store.Resolve(context.Background(), second); !errors.Is(err, redis.Nil) {
		t.Fatalf("user session error = %v", err)
	}
}

func newTestStore(t *testing.T) (*Store, *redis.Client, *time.Time) {
	t.Helper()
	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mini.Close)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	mini.SetTime(now)
	store := NewStore(client, 24*time.Hour, 7*24*time.Hour)
	store.now = func() time.Time { return now }
	randomBytes := make([]byte, 128)
	for index := range randomBytes {
		randomBytes[index] = byte(index)
	}
	store.random = bytes.NewReader(randomBytes)
	return store, client, &now
}
