package irc

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

var reserveRateSlot = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local start = now_ms
if current > start then start = current end
local next = start + ARGV[1]
redis.call('SET', KEYS[1], next, 'PX', ARGV[1] + (start - now_ms))
return start - now_ms
`)

var releaseDeliveryGate = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var acquireDeliveryGate = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current == ARGV[1] then
  return 1
end
if current then
  return 0
end
if redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2], 'NX') then
  return 1
end
return 0
`)

type RedisDeliveryGate struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

func NewRedisDeliveryGate(client *redis.Client, prefix string, ttl time.Duration) *RedisDeliveryGate {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &RedisDeliveryGate{client: client, prefix: prefix, ttl: ttl}
}

func (g *RedisDeliveryGate) key(channel string) string { return g.prefix + channel }

func (g *RedisDeliveryGate) Acquire(ctx context.Context, job Job) error {
	if g == nil || g.client == nil {
		return nil
	}
	key := g.key(job.Channel)
	result, err := acquireDeliveryGate.Run(ctx, g.client, []string{key}, deliveryIdentity(job), g.ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return ErrChannelBusy
	}
	return nil
}

func (g *RedisDeliveryGate) Release(job Job) {
	if g == nil || g.client == nil || job.ID == "" || job.Channel == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = releaseDeliveryGate.Run(ctx, g.client, []string{g.key(job.Channel)}, deliveryIdentity(job)).Result()
}

func deliveryIdentity(job Job) string {
	if job.LeaseToken == "" {
		return job.ID
	}
	return job.ID + ":" + job.LeaseToken
}

// RedisRateLimiter coordinates Bancho command spacing across server instances.
type RedisRateLimiter struct {
	client   *redis.Client
	key      string
	interval time.Duration
}

func NewRedisRateLimiter(client *redis.Client, key string, interval time.Duration) *RedisRateLimiter {
	if interval <= 0 {
		interval = time.Second
	}
	return &RedisRateLimiter{client: client, key: key, interval: interval}
}

func (l *RedisRateLimiter) Wait(ctx context.Context) error {
	if l == nil || l.client == nil {
		return nil
	}
	waitMS, err := reserveRateSlot.Run(ctx, l.client, []string{l.key}, l.interval.Milliseconds()).Int64()
	if err != nil {
		return err
	}
	if waitMS <= 0 {
		return nil
	}
	timer := time.NewTimer(time.Duration(waitMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
