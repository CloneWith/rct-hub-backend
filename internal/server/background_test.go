package server

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeriodicTasksDoNotBlockEachOther(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	blockedStarted := make(chan struct{})
	otherRan := make(chan struct{}, 1)

	startPeriodicTask(ctx, &workers, time.Millisecond, func(ctx context.Context) error {
		select {
		case <-blockedStarted:
		default:
			close(blockedStarted)
		}
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	startPeriodicTask(ctx, &workers, time.Millisecond, func(context.Context) error {
		select {
		case otherRan <- struct{}{}:
		default:
		}
		return nil
	}, nil)

	select {
	case <-blockedStarted:
	case <-time.After(time.Second):
		t.Fatal("blocking task did not start")
	}
	select {
	case <-otherRan:
	case <-time.After(time.Second):
		t.Fatal("independent task was delayed by a blocked task")
	}

	cancel()
	waitForWorkers(t, &workers)
}

func TestPeriodicTaskRecoversAfterOneRunFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	var attempts atomic.Int32
	errorsSeen := make(chan error, 1)
	succeeded := make(chan struct{}, 1)

	startPeriodicTask(ctx, &workers, time.Millisecond, func(context.Context) error {
		if attempts.Add(1) == 1 {
			return errors.New("temporary failure")
		}
		select {
		case succeeded <- struct{}{}:
		default:
		}
		return nil
	}, func(err error) {
		select {
		case errorsSeen <- err:
		default:
		}
	})

	select {
	case err := <-errorsSeen:
		if err == nil || err.Error() != "temporary failure" {
			t.Fatalf("reported error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("task failure was not reported")
	}
	select {
	case <-succeeded:
	case <-time.After(time.Second):
		t.Fatal("periodic task did not run again after a failure")
	}

	cancel()
	waitForWorkers(t, &workers)
}

func waitForWorkers(t *testing.T, workers *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background workers did not stop after cancellation")
	}
}
