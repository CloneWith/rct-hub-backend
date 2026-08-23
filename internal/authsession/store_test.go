package authsession

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
)

func TestStoreCreatesOpaqueSessionAndResolvesClaims(t *testing.T) {
	store, client, _, _ := newTestStore(t)
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
			store, _, now, _ := newTestStore(t)
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
	store, _, _, _ := newTestStore(t)
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

func newTestStore(t *testing.T) (*Store, *redis.Client, *time.Time, *miniredis.Miniredis) {
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
	return store, client, &now, mini
}

// advance moves both the store clock and the miniredis clock so TTL semantics
// stay consistent with the store's now(). miniredis has two clocks: FastForward
// decreases the TTL of every existing key, while SetTime re-bases how EXPIREAT
// timestamps are converted to TTLs. Both must move together to mirror real time.
func advance(now *time.Time, mini *miniredis.Miniredis, duration time.Duration) {
	*now = (*now).Add(duration)
	mini.FastForward(duration)
	mini.SetTime(*now)
}

func TestStoreSlidesIdleWindowWhenRenewalThresholdIsReached(t *testing.T) {
	store, _, now, mini := newTestStore(t)
	user := &domain.User{ID: bson.NewObjectID(), OnlineID: 1001, Username: "captain"}
	secret, err := store.Create(context.Background(), user)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	key := sessionKeyPrefix + sessionDigest(secret)
	createdAt := (*now).Unix()

	// Within the renewal threshold no write happens and the TTL stays put.
	advance(now, mini, 6*time.Hour) // 6h < idle/2 (12h)
	_, renewed, err := store.ResolveWithRenewal(context.Background(), secret)
	if err != nil {
		t.Fatalf("ResolveWithRenewal: %v", err)
	}
	if renewed {
		t.Fatal("session was renewed before the threshold")
	}
	if lastSeen, _ := store.redis.HGet(context.Background(), key, "last_seen").Result(); lastSeen != strconv.FormatInt(createdAt, 10) {
		t.Fatalf("last_seen = %q, want original creation time %d", lastSeen, createdAt)
	}
	ttlBefore, err := store.redis.TTL(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttlBefore != 18*time.Hour {
		t.Fatalf("TTL = %v, want 18h (24h idle minus 6h elapsed)", ttlBefore)
	}

	// Crossing the threshold slides the window and re-arms the TTL.
	advance(now, mini, 7*time.Hour) // now 13h after creation > 12h threshold
	_, renewed, err = store.ResolveWithRenewal(context.Background(), secret)
	if err != nil {
		t.Fatalf("ResolveWithRenewal: %v", err)
	}
	if !renewed {
		t.Fatal("session should be renewed after the threshold")
	}
	if lastSeen, _ := store.redis.HGet(context.Background(), key, "last_seen").Result(); lastSeen != strconv.FormatInt((*now).Unix(), 10) {
		t.Fatalf("last_seen = %q, want %d", lastSeen, (*now).Unix())
	}
	ttlAfter, err := store.redis.TTL(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttlAfter != 24*time.Hour {
		t.Fatalf("TTL = %v, want full 24h idle window", ttlAfter)
	}
}

func TestStoreActiveUserNeverTimesOutUntilAbsoluteDeadline(t *testing.T) {
	store, _, now, mini := newTestStore(t)
	user := &domain.User{ID: bson.NewObjectID(), OnlineID: 1001, Username: "captain"}
	secret, err := store.Create(context.Background(), user)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The user comes back every 13h — past the 12h renewal threshold but well
	// inside the 24h idle window. Each visit must slide the session instead of
	// letting it expire, for as long as the absolute deadline allows.
	for hop := range 8 {
		advance(now, mini, 13*time.Hour)
		claims, renewed, err := store.ResolveWithRenewal(context.Background(), secret)
		if err != nil {
			t.Fatalf("hop %d: ResolveWithRenewal: %v", hop, err)
		}
		if claims == nil || claims.UserID != user.ID.Hex() {
			t.Fatalf("hop %d: claims = %+v", hop, claims)
		}
		if !renewed {
			t.Fatalf("hop %d: expected renewal after 13h idle", hop)
		}
	}

	// The absolute deadline is a hard upper bound even for active users.
	advance(now, mini, 7*24*time.Hour)
	if _, _, err := store.ResolveWithRenewal(context.Background(), secret); !errors.Is(err, redis.Nil) {
		t.Fatalf("Resolve error = %v, want redis.Nil past absolute deadline", err)
	}
}
