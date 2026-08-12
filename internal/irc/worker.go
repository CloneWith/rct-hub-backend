package irc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Job struct {
	ID             string
	MatchID        string
	Channel        string
	Kind           string
	AckTarget      string
	Sequence       uint64
	Payload        []byte
	Status         JobStatus
	Attempts       int
	NextTryAt      time.Time
	SentAt         time.Time
	AckDeadline    time.Time
	AcknowledgedAt time.Time
	LastError      string
	LeaseToken     string
	AutomaticRetry bool
}

type JobStatus string

const (
	JobPending      JobStatus = "PENDING"
	JobSending      JobStatus = "SENDING"
	JobSent         JobStatus = "SENT"
	JobAcknowledged JobStatus = "ACKNOWLEDGED"
	JobFailed       JobStatus = "FAILED"
	JobCancelled    JobStatus = "CANCELLED"
)

var ErrJobObsolete = errors.New("IRC job no longer belongs to the current match room")

const maxAutomaticAttempts = 5

type Delivery struct{ Status JobStatus }

type JobStore interface {
	Claim(context.Context, time.Time, time.Time) (*Job, error)
	MarkSent(context.Context, string, string, time.Time, time.Time) error
	Ack(context.Context, string, string, time.Time) error
	Fail(context.Context, string, string, string, time.Time) error
	Reject(context.Context, string, string, string) error
	Cancel(context.Context, string, string, string) error
}

type Sender interface {
	Send(context.Context, Job) (Delivery, error)
}

type RateLimiter interface {
	Wait(context.Context) error
}

type JobValidator func(context.Context, Job) error

type pendingForgetter interface {
	ForgetDelivery(string)
}

type deliveryCanceller interface {
	CancelDelivery(Job)
}

type Worker struct {
	store    JobStore
	sender   Sender
	interval time.Duration
	ackWait  time.Duration
	lease    time.Duration
	limiter  RateLimiter
	validate JobValidator
	mu       sync.Mutex
	lastSend time.Time
}

func (w *Worker) WithRateLimiter(limiter RateLimiter) *Worker {
	w.limiter = limiter
	return w
}

func (w *Worker) WithValidator(validate JobValidator) *Worker {
	w.validate = validate
	return w
}

func NewWorker(store JobStore, sender Sender, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = time.Second
	}
	return &Worker{store: store, sender: sender, interval: interval, ackWait: 15 * time.Second, lease: 30 * time.Second}
}

func (w *Worker) WithTimeouts(ackWait, lease time.Duration) *Worker {
	if ackWait > 0 {
		w.ackWait = ackWait
	}
	if lease > 0 {
		w.lease = lease
	}
	return w
}

func (w *Worker) RunOnce(ctx context.Context) error {
	if w == nil || w.store == nil || w.sender == nil {
		return fmt.Errorf("IRC worker is not configured")
	}
	now := time.Now().UTC()
	job, err := w.store.Claim(ctx, now, now.Add(w.lease))
	if err != nil {
		return err
	}
	if job == nil {
		return nil
	}
	if w.validate != nil {
		if err := w.validate(ctx, *job); err != nil {
			if errors.Is(err, ErrJobObsolete) {
				return w.store.Cancel(ctx, job.ID, job.LeaseToken, err.Error())
			}
			return w.fail(ctx, *job, err)
		}
	}
	if job.Status == JobSent {
		if forgetter, ok := w.sender.(pendingForgetter); ok {
			forgetter.ForgetDelivery(job.ID)
		}
		return w.fail(ctx, *job, errors.New("Bancho acknowledgement timed out"))
	}
	if w.limiter != nil {
		if err := w.limiter.Wait(ctx); err != nil {
			if ctx.Err() != nil {
				return err
			}
			if failErr := w.fail(ctx, *job, err); failErr != nil {
				return errors.Join(err, failErr)
			}
			return err
		}
	} else {
		w.mu.Lock()
		wait := time.Until(w.lastSend.Add(w.interval))
		if wait > 0 {
			w.mu.Unlock()
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			w.mu.Lock()
		}
		w.lastSend = time.Now().UTC()
		w.mu.Unlock()
	}
	sentAt := time.Now().UTC()
	if err := w.store.MarkSent(ctx, job.ID, job.LeaseToken, sentAt, sentAt.Add(w.ackWait)); err != nil {
		if ctx.Err() == nil {
			if failErr := w.fail(ctx, *job, err); failErr != nil {
				return errors.Join(err, failErr)
			}
		}
		return err
	}
	delivery, sendErr := w.sender.Send(ctx, *job)
	if sendErr != nil {
		if canceller, ok := w.sender.(deliveryCanceller); ok {
			canceller.CancelDelivery(*job)
		} else if forgetter, ok := w.sender.(pendingForgetter); ok {
			forgetter.ForgetDelivery(job.ID)
		}
		return w.fail(ctx, *job, sendErr)
	}
	if delivery.Status == JobAcknowledged {
		return w.store.Ack(ctx, job.ID, job.LeaseToken, time.Now().UTC())
	}
	return nil
}

func (w *Worker) fail(ctx context.Context, job Job, err error) error {
	if job.Attempts+1 >= maxAutomaticAttempts {
		return w.store.Reject(ctx, job.ID, job.LeaseToken, fmt.Sprintf("automatic retry limit reached: %v", err))
	}
	return w.store.Fail(ctx, job.ID, job.LeaseToken, err.Error(), time.Now().UTC().Add(backoff(job.Attempts)))
}

func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 6 {
		attempts = 6
	}
	return time.Duration(1<<attempts) * time.Second
}

// BackoffForAttempt is shared by the runtime receipt handler and worker.
func BackoffForAttempt(attempts int) time.Duration { return backoff(attempts) }
