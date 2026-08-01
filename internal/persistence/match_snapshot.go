package persistence

import (
	"encoding/json"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchengine"
)

// MatchSnapshotSchemaVersion identifies the persisted MatchEngine state shape.
const MatchSnapshotSchemaVersion = 1

// MatchSnapshotDocument is the MongoDB representation of one authoritative
// MatchEngine snapshot. State is an embedded BSON document produced through
// the engine's tested JSON recovery contract, keeping BSON concerns outside
// the pure engine package without hiding the aggregate in a binary blob.
type MatchSnapshotDocument struct {
	MatchID       bson.ObjectID `bson:"_id"`
	SchemaVersion int           `bson:"schema_version"`
	MatchVersion  uint64        `bson:"match_version"`
	State         bson.Raw      `bson:"state"`
	UpdatedAt     time.Time     `bson:"updated_at"`
}

// NewMatchSnapshotDocument serializes a MatchEngine state for persistence.
func NewMatchSnapshotDocument(matchID bson.ObjectID, state matchengine.State, updatedAt time.Time) (MatchSnapshotDocument, error) {
	if matchID == bson.NilObjectID {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: match ID is required")
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: encode state: %w", err)
	}
	var stateDocument bson.Raw
	if err := bson.UnmarshalExtJSON(stateJSON, false, &stateDocument); err != nil {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: convert state to BSON: %w", err)
	}
	return MatchSnapshotDocument{
		MatchID:       matchID,
		SchemaVersion: MatchSnapshotSchemaVersion,
		MatchVersion:  state.Version,
		State:         stateDocument,
		UpdatedAt:     updatedAt.UTC(),
	}, nil
}

// DecodeState validates the persistence envelope and restores engine state.
func (d MatchSnapshotDocument) DecodeState() (matchengine.State, error) {
	if d.MatchID == bson.NilObjectID {
		return matchengine.State{}, fmt.Errorf("match snapshot: match ID is required")
	}
	if d.SchemaVersion != MatchSnapshotSchemaVersion {
		return matchengine.State{}, fmt.Errorf("match snapshot: unsupported schema version %d", d.SchemaVersion)
	}
	if len(d.State) == 0 {
		return matchengine.State{}, fmt.Errorf("match snapshot: state is missing")
	}
	if err := d.State.Validate(); err != nil {
		return matchengine.State{}, fmt.Errorf("match snapshot: invalid BSON state: %w", err)
	}
	stateJSON, err := bson.MarshalExtJSON(d.State, false, false)
	if err != nil {
		return matchengine.State{}, fmt.Errorf("match snapshot: convert state from BSON: %w", err)
	}
	var state matchengine.State
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		return matchengine.State{}, fmt.Errorf("match snapshot: decode state: %w", err)
	}
	if state.Version != d.MatchVersion {
		return matchengine.State{}, fmt.Errorf(
			"match snapshot: envelope version %d does not match state version %d",
			d.MatchVersion,
			state.Version,
		)
	}
	return state, nil
}
