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
)

var resolveScript = redis.NewScript(`
local values = redis.call("HMGET", KEYS[1], "user_id", "osu_id", "username", "roles", "absolute_at", "last_seen")
if not values[1] then
  return {}
end
local now = tonumber(ARGV[1])
local idle = tonumber(ARGV[2])
local absolute = tonumber(values[5])
local last_seen = tonumber(values[6])
if now >= absolute or now - last_seen >= idle then
  redis.call("DEL", KEYS[1])
  return {}
end
local expires_at = math.min(absolute, now + idle)
redis.call("HSET", KEYS[1], "last_seen", now)
redis.call("EXPIREAT", KEYS[1], expires_at)
return values
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

type Store struct {
	redis    *redis.Client
	idle     time.Duration
	absolute time.Duration
	now      func() time.Time
	random   io.Reader
}

func NewStore(client *redis.Client, idle, absolute time.Duration) *Store {
	return &Store{redis: client, idle: idle, absolute: absolute, now: time.Now, random: rand.Reader}
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

func (s *Store) Resolve(ctx context.Context, secret string) (*jwtutil.Claims, error) {
	if s == nil || s.redis == nil || secret == "" {
		return nil, redis.Nil
	}
	result, err := resolveScript.Run(ctx, s.redis, []string{sessionKeyPrefix + sessionDigest(secret)}, s.now().UTC().Unix(), int64(s.idle/time.Second)).Result()
	if err != nil {
		return nil, err
	}
	values, ok := result.([]any)
	if !ok || len(values) == 0 {
		return nil, redis.Nil
	}
	if len(values) != 6 {
		return nil, errors.New("invalid browser session record")
	}
	userID, err := redisString(values[0])
	if err != nil {
		return nil, err
	}
	osuIDText, err := redisString(values[1])
	if err != nil {
		return nil, err
	}
	osuID, err := strconv.ParseInt(osuIDText, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid browser session osu id: %w", err)
	}
	username, err := redisString(values[2])
	if err != nil {
		return nil, err
	}
	rolesJSON, err := redisString(values[3])
	if err != nil {
		return nil, err
	}
	var roles []domain.UserRole
	if err := json.Unmarshal([]byte(rolesJSON), &roles); err != nil {
		return nil, fmt.Errorf("invalid browser session roles: %w", err)
	}
	return &jwtutil.Claims{UserID: userID, OsuID: osuID, Username: username, Roles: roles}, nil
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
