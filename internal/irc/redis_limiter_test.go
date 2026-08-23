package irc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisRateLimiterReservesSharedSlots(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	first := NewRedisRateLimiter(client, "irc:test", 40*time.Millisecond)
	second := NewRedisRateLimiter(client, "irc:test", 40*time.Millisecond)
	if err := first.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := second.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 30*time.Millisecond {
		t.Fatalf("shared limiter waited only %s", elapsed)
	}
}

func TestRedisDeliveryGatePreventsCrossInstanceAmbiguousACK(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	first := NewRedisDeliveryGate(client, "irc:gate:", time.Minute)
	second := NewRedisDeliveryGate(client, "irc:gate:", time.Minute)
	job := Job{ID: "job-1", Channel: "#mp_42"}
	if err := first.Acquire(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := second.Acquire(context.Background(), Job{ID: "job-2", Channel: job.Channel}); err != ErrChannelBusy {
		t.Fatalf("second acquire = %v, want ErrChannelBusy", err)
	}
	first.Release(job)
	if err := second.Acquire(context.Background(), Job{ID: "job-2", Channel: job.Channel}); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestRedisDeliveryGateAcquireIsAtomicAcrossInstances(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	first := NewRedisDeliveryGate(client, "irc:gate:", time.Minute)
	second := NewRedisDeliveryGate(client, "irc:gate:", time.Minute)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- first.Acquire(context.Background(), Job{ID: "job-1", Channel: "#mp_42"}) }()
	go func() { <-start; results <- second.Acquire(context.Background(), Job{ID: "job-2", Channel: "#mp_42"}) }()
	close(start)
	var successes int
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent gate acquisitions succeeded %d times, want exactly one", successes)
	}
}

func TestRedisDeliveryGateDoesNotReuseOrReleaseAnotherLease(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	gate := NewRedisDeliveryGate(client, "irc:gate:", time.Minute)
	first := Job{ID: "job-1", Channel: "#mp_42", LeaseToken: "lease-1"}
	second := Job{ID: "job-1", Channel: "#mp_42", LeaseToken: "lease-2"}
	if err := gate.Acquire(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := gate.Acquire(context.Background(), second); !errors.Is(err, ErrChannelBusy) {
		t.Fatalf("new lease reused old gate: %v", err)
	}
	gate.Release(second)
	if err := gate.Acquire(context.Background(), Job{ID: "job-2", Channel: "#mp_42", LeaseToken: "lease-3"}); !errors.Is(err, ErrChannelBusy) {
		t.Fatalf("old release removed current lease: %v", err)
	}
	gate.Release(first)
	if err := gate.Acquire(context.Background(), second); err != nil {
		t.Fatalf("new lease could not acquire after owner release: %v", err)
	}
}
