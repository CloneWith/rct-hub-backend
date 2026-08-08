package matchcommand

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchengine"
)

type Disposition string

const (
	DispositionApplied  Disposition = "APPLIED"
	DispositionReplayed Disposition = "REPLAYED"
)

type Request struct {
	MatchID         bson.ObjectID
	ExpectedVersion uint64
	CommandID       string
	CallerOsuID     int64
	Command         matchengine.Command
}

type AuthorizedActor struct {
	UserID          bson.ObjectID
	OsuID           int64
	GlobalRoles     []string
	EngineActor     matchengine.Actor
	AdminOverride   bool
	RefereeOverride bool
	Reason          string
}

type Envelope struct {
	MatchID         bson.ObjectID
	ExpectedVersion uint64
	CommandID       string
	CommandType     string
	RequestHash     string
	PayloadJSON     []byte
	OccurredAt      time.Time
}

type Result struct {
	CommandID        string
	Disposition      Disposition
	PreviousVersion  uint64
	ResultingVersion uint64
	State            matchengine.State
	Events           []CommittedEvent
}

const EventSchemaVersion = 1

// CommittedEvent is the durable event envelope returned to API clients and
// later published from the outbox. Its identity is stable across retries.
type CommittedEvent struct {
	EventID          string                `json:"eventId"`
	Sequence         uint64                `json:"sequence"`
	ResultingVersion uint64                `json:"resultingVersion"`
	Type             matchengine.EventType `json:"type"`
	OccurredAt       time.Time             `json:"occurredAt"`
	Actor            EventActor            `json:"actor"`
	Payload          matchengine.Event     `json:"payload"`
}

type EventActor struct {
	OsuID           int64                  `json:"osuId"`
	Capability      matchengine.Capability `json:"capability"`
	Team            *matchengine.TeamSide  `json:"team,omitempty"`
	AdminOverride   bool                   `json:"adminOverride"`
	RefereeOverride bool                   `json:"refereeOverride"`
}

type AuthorizeFunc func(context.Context) (AuthorizedActor, error)
type ExecuteFunc func(matchengine.State, AuthorizedActor) (matchengine.Transition, error)

type TransactionStore interface {
	Apply(context.Context, Envelope, AuthorizeFunc, ExecuteFunc) (Result, error)
}
