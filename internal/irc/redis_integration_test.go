package irc

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const integrationRedisAddrEnv = "REDIS_TEST_ADDR"

func integrationRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv(integrationRedisAddrEnv)
	if addr == "" {
		t.Skipf("set %s to run Redis integration tests", integrationRedisAddrEnv)
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("redis.Ping: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestRedisIntegrationDeliveryGateIsAtomicAcrossInstances(t *testing.T) {
	client := integrationRedis(t)
	prefix := "rct:test:gate:" + uuid.NewString() + ":"
	first := NewRedisDeliveryGate(client, prefix, time.Minute)
	second := NewRedisDeliveryGate(client, prefix, time.Minute)
	channel := "#mp_" + uuid.NewString()
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		results <- first.Acquire(context.Background(), Job{ID: "job-1", Channel: channel, LeaseToken: "lease-1"})
	}()
	go func() {
		<-start
		results <- second.Acquire(context.Background(), Job{ID: "job-2", Channel: channel, LeaseToken: "lease-2"})
	}()
	close(start)

	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrChannelBusy) {
			t.Fatalf("delivery gate acquire error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent delivery gate acquisitions succeeded %d times, want exactly one", successes)
	}
}

func TestRedisIntegrationDeliveryGateDoesNotReleaseAnotherLease(t *testing.T) {
	client := integrationRedis(t)
	prefix := "rct:test:gate:" + uuid.NewString() + ":"
	gate := NewRedisDeliveryGate(client, prefix, time.Minute)
	channel := "#mp_" + uuid.NewString()
	first := Job{ID: "job-1", Channel: channel, LeaseToken: "lease-1"}
	second := Job{ID: "job-1", Channel: channel, LeaseToken: "lease-2"}
	if err := gate.Acquire(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := gate.Acquire(context.Background(), second); !errors.Is(err, ErrChannelBusy) {
		t.Fatalf("new lease reused old gate: %v", err)
	}
	gate.Release(second)
	if err := gate.Acquire(context.Background(), Job{ID: "job-2", Channel: channel, LeaseToken: "lease-3"}); !errors.Is(err, ErrChannelBusy) {
		t.Fatalf("old release removed current lease: %v", err)
	}
	gate.Release(first)
	if err := gate.Acquire(context.Background(), second); err != nil {
		t.Fatalf("new lease could not acquire after owner release: %v", err)
	}
}

func TestRedisIntegrationRateLimiterCoordinatesInstances(t *testing.T) {
	client := integrationRedis(t)
	key := "rct:test:rate:" + uuid.NewString()
	first := NewRedisRateLimiter(client, key, 40*time.Millisecond)
	second := NewRedisRateLimiter(client, key, 40*time.Millisecond)
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 25*time.Millisecond {
		t.Fatalf("shared rate limiter waited only %s", elapsed)
	}
}
