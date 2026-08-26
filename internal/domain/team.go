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

// Snapshot builds the immutable per-side roster snapshot embedded in a match
// when the match starts. Color derives from the side, not the entity.
func (t *Team) Snapshot(side TeamSide) TeamSnapshot {
	snapshot := TeamSnapshot{
		ID:           t.ID,
		Side:         side,
		Name:         t.Name,
		Color:        SideColor(side),
		LeaderID:     DerefInt64(t.LeaderID, 0),
		StrategistID: DerefInt64(t.StrategistID, 0),
		Players:      append([]int64(nil), t.Players...),
	}
	if t.Description != nil {
		snapshot.Description = *t.Description
	}
	if t.Seed != nil {
		snapshot.Seed = *t.Seed
	}
	return snapshot
}

// SideColor returns the canonical display color for a team side.
func SideColor(side TeamSide) string {
	if side == TeamSideBlue {
		return "#3b82f6"
	}
	return "#ef4444"
}

// StrategistSide reports which side the given osu user is the assigned
// strategist of. ok is false when the user is not assigned to exactly one
// side; unlinked teams count as unassigned.
func StrategistSide(redTeam, blueTeam *Team, osuID int64) (TeamSide, bool) {
	return sideByAssignment(
		redTeam != nil && redTeam.StrategistID != nil && *redTeam.StrategistID == osuID,
		blueTeam != nil && blueTeam.StrategistID != nil && *blueTeam.StrategistID == osuID,
	)
}

// CaptainSide reports which side the given osu user is the captain (leader)
// of. ok is false when the user is not assigned to exactly one side.
func CaptainSide(redTeam, blueTeam *Team, osuID int64) (TeamSide, bool) {
	return sideByAssignment(
		redTeam != nil && redTeam.LeaderID != nil && *redTeam.LeaderID == osuID,
		blueTeam != nil && blueTeam.LeaderID != nil && *blueTeam.LeaderID == osuID,
	)
}

func sideByAssignment(red, blue bool) (TeamSide, bool) {
	switch {
	case red && !blue:
		return TeamSideRed, true
	case blue && !red:
		return TeamSideBlue, true
	default:
		return "", false
	}
}

// DerefInt64 returns the dereferenced value or fallback when p is nil.
func DerefInt64(p *int64, fallback int64) int64 {
	if p == nil {
		return fallback
	}
	return *p
}
