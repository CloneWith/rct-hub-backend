package domain

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Team is the manageable team entity. A team becomes referenceable by a room
// only once it is ready (see IsReady): it must have both a leader and a
// strategist. Regular players are optional.
type Team struct {
	ID           bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Name         string        `json:"name" bson:"name"` // required
	Description  *string       `json:"description,omitempty" bson:"description,omitempty"`
	Seed         *string       `json:"seed,omitempty" bson:"seed,omitempty"` // seed string (e.g. "A1")
	LeaderID     *int64        `json:"leader_id,omitempty" bson:"leader_id,omitempty"`
	StrategistID *int64        `json:"strategist_id,omitempty" bson:"strategist_id,omitempty"`
	Players      []int64       `json:"players" bson:"players"` // osu uids, leader/strategist included when set
	CreatedAt    time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at" bson:"updated_at"`
}

// IsReady reports whether the team satisfies the minimum requirements for
// being linked to a room: it must have both a leader and a strategist.
// This rule applies to every room type.
func (t *Team) IsReady() bool {
	return t.LeaderID != nil && t.StrategistID != nil
}
