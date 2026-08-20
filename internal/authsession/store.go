package authsession

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"rctHubBackend/internal/domain"
	"rctHubBackend/pkg/jwtutil"
)

const (
	sessionKeyPrefix = "auth:session:"
	userKeyPrefix    = "auth:user-sessions:"

	// renewIntervalDivisor throttles sliding renewals to at most one write per
	// idle/renewIntervalDivisor window. Every Resolve of an active session that
	// passed the threshold bumps last_seen and re-arms the Redis TTL, so the key
	// can never expire while the user keeps making requests (threshold < idle).
	// Throttling keeps renewal writes and Set-Cookie refreshes off the hot path.
	renewIntervalDivisor = 2
)

// resolveScript validates a session against its idle and absolute deadlines and
// slides the idle window in place when the renewal threshold has been reached.
// Returning the session fields together with a renewed flag lets HTTP layers
// refresh the browser cookie exactly when the server-side window was extended.
//
// Keys:   KEYS[1] = auth:session:<digest>
// Args:   ARGV[1] = now (unix seconds), ARGV[2] = idle (seconds),
//
//	ARGV[3] = renew threshold (seconds, must be < idle)
//
// Return: {}  → missing or expired (key already deleted)
//
//	{user_id, osu_id, username, roles, absolute_at, renewed}
var resolveScript = redis.NewScript(`
local values = redis.call("HMGET", KEYS[1], "user_id", "osu_id", "username", "roles", "absolute_at", "last_seen")
if not values[1] then
  return {}
end
local now = tonumber(ARGV[1])
local idle = tonumber(ARGV[2])
local renew_threshold = tonumber(ARGV[3])
local absolute = tonumber(values[5])
local last_seen = tonumber(values[6])
if now >= absolute or now - last_seen >= idle then
  redis.call("DEL", KEYS[1])
  return {}
end
local renewed = 0
if now - last_seen >= renew_threshold then
  local expires_at = math.min(absolute, now + idle)
  redis.call("HSET", KEYS[1], "last_seen", now)
  redis.call("EXPIREAT", KEYS[1], expires_at)
  renewed = 1
end
return {values[1], values[2], values[3], values[4], values[5], renewed}
`)

var revokeUserScript = redis.NewScript(`
local digests = redis.call("SMEMBERS", KEYS[1])
for _, digest in ipairs(digests) do
  redis.call("DEL", ARGV[1] .. digest)
end
redis.call("DEL", KEYS[1])
return #digests
`)

type Resolver interface {
	Resolve(ctx context.Context, secret string) (*jwtutil.Claims, error)
}

type Revoker interface {
	RevokeUser(ctx context.Context, userID string) error
}

type Manager interface {
	Resolver
	Revoker
	Create(ctx context.Context, user *domain.User) (string, error)
	Revoke(ctx context.Context, secret string) error
}

// RenewalResolver is implemented by stores that can report whether a session
// renewal happened during resolution, so HTTP layers can refresh the client
// cookie in lockstep with the server-side sliding window.
type RenewalResolver interface {
	Resolver
	ResolveWithRenewal(ctx context.Context, secret string) (*jwtutil.Claims, bool, error)
}

type Store struct {
	redis          *redis.Client
	idle           time.Duration
	absolute       time.Duration
	renewThreshold time.Duration
	now            func() time.Time
	random         io.Reader
}

func NewStore(client *redis.Client, idle, absolute time.Duration) *Store {
	return &Store{
		redis:          client,
		idle:           idle,
		absolute:       absolute,
		renewThreshold: idle / renewIntervalDivisor,
		now:            time.Now,
		random:         rand.Reader,
	}
}

func (s *Store) Create(ctx context.Context, user *domain.User) (string, error) {
	if s == nil || s.redis == nil || user == nil || user.ID.IsZero() || s.idle <= 0 || s.absolute <= 0 {
		return "", errors.New("browser session store is not configured")
	}
	secretBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.random, secretBytes); err != nil {
		return "", fmt.Errorf("generate browser session: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	digest := sessionDigest(secret)
	now := s.now().UTC()
	absoluteAt := now.Add(s.absolute)
	expiresAt := now.Add(s.idle)
	if absoluteAt.Before(expiresAt) {
		expiresAt = absoluteAt
	}
	roles, err := json.Marshal(user.Roles)
	if err != nil {
		return "", fmt.Errorf("encode browser session roles: %w", err)
	}
	sessionKey := sessionKeyPrefix + digest
	userKey := userKeyPrefix + user.ID.Hex()
	_, err = s.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, sessionKey, map[string]any{
			"user_id": user.ID.Hex(), "osu_id": user.OnlineID, "username": user.Username,
			"roles": string(roles), "absolute_at": absoluteAt.Unix(), "last_seen": now.Unix(),
		})
		pipe.ExpireAt(ctx, sessionKey, expiresAt)
		pipe.SAdd(ctx, userKey, digest)
		pipe.ExpireAt(ctx, userKey, absoluteAt)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("store browser session: %w", err)
	}
	return secret, nil
}

// Resolve checks a browser session secret and returns its claims. The Redis key
// is lazily deleted when the idle or absolute deadline has passed, which is what
// bounds zombie sessions: nothing needs to run in the background.
func (s *Store) Resolve(ctx context.Context, secret string) (*jwtutil.Claims, error) {
	claims, _, err := s.ResolveWithRenewal(ctx, secret)
	return claims, err
}

// ResolveWithRenewal behaves like Resolve but also reports whether the session
// was slid this call (renewed == true). Renewal updates last_seen and re-arms
// the Redis TTL at most once per renewThreshold, keeping writes off the hot path.
func (s *Store) ResolveWithRenewal(ctx context.Context, secret string) (*jwtutil.Claims, bool, error) {
	if s == nil || s.redis == nil || secret == "" {
		return nil, false, redis.Nil
	}
	result, err := resolveScript.Run(ctx, s.redis, []string{sessionKeyPrefix + sessionDigest(secret)},
		s.now().UTC().Unix(), int64(s.idle/time.Second), int64(s.renewThreshold/time.Second)).Result()
	if err != nil {
		return nil, false, err
	}
	values, ok := result.([]any)
	if !ok || len(values) == 0 {
		return nil, false, redis.Nil
	}
	if len(values) != 6 {
		return nil, false, errors.New("invalid browser session record")
	}
	renewed := false
	if flag, ok := values[5].(int64); ok && flag == 1 {
		renewed = true
	}
	userID, err := redisString(values[0])
	if err != nil {
		return nil, false, err
	}
	osuIDText, err := redisString(values[1])
	if err != nil {
		return nil, false, err
	}
	osuID, err := strconv.ParseInt(osuIDText, 10, 64)
	if err != nil {
		return nil, false, fmt.Errorf("invalid browser session osu id: %w", err)
	}
	username, err := redisString(values[2])
	if err != nil {
		return nil, false, err
	}
	rolesJSON, err := redisString(values[3])
	if err != nil {
		return nil, false, err
	}
	var roles []domain.UserRole
	if err := json.Unmarshal([]byte(rolesJSON), &roles); err != nil {
		return nil, false, fmt.Errorf("invalid browser session roles: %w", err)
	}
	return &jwtutil.Claims{UserID: userID, OsuID: osuID, Username: username, Roles: roles}, renewed, nil
}

func (s *Store) Revoke(ctx context.Context, secret string) error {
	if s == nil || s.redis == nil || secret == "" {
		return nil
	}
	digest := sessionDigest(secret)
	key := sessionKeyPrefix + digest
	userID, err := s.redis.HGet(ctx, key, "user_id").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("read browser session for revocation: %w", err)
	}
	_, err = s.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		if userID != "" {
			pipe.SRem(ctx, userKeyPrefix+userID, digest)
		}
		return nil
	})
	return err
}

func (s *Store) RevokeUser(ctx context.Context, userID string) error {
	if s == nil || s.redis == nil || userID == "" {
		return nil
	}
	if err := revokeUserScript.Run(ctx, s.redis, []string{userKeyPrefix + userID}, sessionKeyPrefix).Err(); err != nil {
		return fmt.Errorf("revoke browser sessions: %w", err)
	}
	return nil
}

func sessionDigest(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

func redisString(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return "", fmt.Errorf("invalid browser session value %T", value)
	}
}
