package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
)

// MatchCommandDriver is the narrow door RoomService uses to push match
// commands when no human caller is in the loop (today: the casual / private
// auto-start path, fired after both strategists press "Ready"). It MUST
// stay RFC-3 thin: callers do not see events, do not see state — they only
// express "this is what should happen next on the engine".
type MatchCommandDriver interface {
	StartMatchSystem(ctx context.Context, matchID bson.ObjectID, expectedVersion uint64, now time.Time) error
}

// matchCommandDriver is the default implementation backed by the real
// Orchestrator. Wiring lives here so service.RoomService can stay free of
// a direct dependency on the orchestrator package (and its many readers).
type matchCommandDriver struct {
	orchestrator *matchcommand.Orchestrator
	now          func() time.Time
	log          *zap.Logger
}

// NewMatchCommandDriver wraps the orchestrator as a MatchCommandDriver.
// Returns nil when orchestrator is nil so RoomService can degrade to a
// best-effort "no auto-start available" behavior in unit tests.
func NewMatchCommandDriver(orchestrator *matchcommand.Orchestrator, log *zap.Logger) MatchCommandDriver {
	if orchestrator == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &matchCommandDriver{
		orchestrator: orchestrator,
		now:          func() time.Time { return time.Now().UTC() },
		log:          log,
	}
}

func (d *matchCommandDriver) StartMatchSystem(ctx context.Context, matchID bson.ObjectID, expectedVersion uint64, now time.Time) error {
	if d == nil || d.orchestrator == nil {
		return nil
	}
	timestamp := now
	if timestamp.IsZero() {
		timestamp = d.now()
	}
	request := matchcommand.Request{
		MatchID:         matchID,
		ExpectedVersion: expectedVersion,
		CommandID:       uuid.NewString(),
		Command:         matchengine.StartMatch{},
		System:          true,
	}
	result, err := d.orchestrator.Execute(ctx, request)
	if err != nil {
		d.log.Warn("auto-start match command failed",
			zap.String("match_id", matchID.Hex()),
			zap.Error(err),
		)
		return err
	}
	d.log.Info("auto-start match command applied",
		zap.String("match_id", matchID.Hex()),
		zap.Uint64("resulting_version", result.ResultingVersion),
	)
	return nil
}
