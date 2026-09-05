package service

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// MatchCommandDriver is the narrow door RoomService uses to push match
// commands when no human caller is in the loop (today: the casual / private
// auto-start path, fired after both strategists press "Ready"). It MUST
// stay RFC-3 thin: callers do not see events, do not see state — they only
// express "this is what should happen next on the engine".
//
// Concrete implementation lives in internal/matchcommand/driver.go to keep
// the matchcommand package off the service layer's import graph (the verify
// tool enforces this boundary; see tools/verify/checkMatchCommandBoundaries).
type MatchCommandDriver interface {
	StartMatchSystem(ctx context.Context, matchID bson.ObjectID, expectedVersion uint64, now time.Time) error
}
