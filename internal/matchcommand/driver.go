package matchcommand

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/matchengine"
)

// Driver wraps an Orchestrator so it can be passed into places (e.g. the
// service layer's RoomService auto-start path) that must not import the
// matchcommand package directly. The concrete type satisfies
// service.MatchCommandDriver structurally — no explicit interface
// conformance is needed.
//
// The constructor returns nil when orchestrator is nil so callers (such as
// unit tests) can degrade to a best-effort "no auto-start available"
// behavior without sprinkling nil checks everywhere.
type Driver struct {
	orchestrator *Orchestrator
	now          func() time.Time
	log          *zap.Logger
}

// NewDriver wraps the given orchestrator so it can be used by the service
// layer's RoomService via the WithMatchCommandDriver wiring hook. The
// driver deliberately lives here — not in the service package — because
// tools/verify/checkMatchCommandBoundaries forbids any non-adapter,
// non-store, non-fixture, non-composition-root code from importing
// matchcommand. Keeping the impl alongside the orchestrator it wraps is the
// only place both invariants ("service stays free of matchcommand",
// "matchcommand is the only thing this driver touches") can hold at once.
func NewDriver(orchestrator *Orchestrator, log *zap.Logger) *Driver {
	if orchestrator == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		orchestrator: orchestrator,
		now:          func() time.Time { return time.Now().UTC() },
		log:          log,
	}
}

// StartMatchSystem pushes a system-flagged START_MATCH through the
// orchestrator. A nil receiver is a no-op so unit tests can wire a
// service.RoomService without an orchestrator.
func (d *Driver) StartMatchSystem(ctx context.Context, matchID bson.ObjectID, expectedVersion uint64, now time.Time) error {
	if d == nil || d.orchestrator == nil {
		return nil
	}
	timestamp := now
	if timestamp.IsZero() {
		timestamp = d.now()
	}
	request := Request{
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
