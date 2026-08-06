package persistence

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchengine"
)

// MatchSnapshotSchemaVersion identifies the persisted MatchEngine state shape.
const MatchSnapshotSchemaVersion = 3

// SnapshotOrigin records how an authoritative aggregate entered the snapshot
// collection. Legacy matches are never converted implicitly.
type SnapshotOrigin string

const (
	SnapshotOriginNative SnapshotOrigin = "NATIVE"
)

// MatchSnapshotDocument is the MongoDB representation of one authoritative
// MatchEngine snapshot. State is an embedded BSON document produced through
// the engine's tested JSON recovery contract, keeping BSON concerns outside
// the pure engine package without hiding the aggregate in a binary blob.
type MatchSnapshotDocument struct {
	MatchID           bson.ObjectID  `bson:"_id"`
	SchemaVersion     int            `bson:"schema_version"`
	MatchVersion      uint64         `bson:"match_version"`
	Origin            SnapshotOrigin `bson:"origin"`
	ConfigurationHash string         `bson:"configuration_hash"`
	State             bson.Raw       `bson:"state"`
	CreatedAt         time.Time      `bson:"created_at"`
	UpdatedAt         time.Time      `bson:"updated_at"`
}

// NewMatchSnapshotDocument serializes a MatchEngine state for persistence.
func NewMatchSnapshotDocument(matchID bson.ObjectID, state matchengine.State, updatedAt time.Time) (MatchSnapshotDocument, error) {
	if matchID == bson.NilObjectID {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: match ID is required")
	}
	if updatedAt.IsZero() {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: timestamp is required")
	}
	if err := matchengine.ValidateState(state); err != nil {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: invalid state: %w", err)
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: encode state: %w", err)
	}
	var stateDocument bson.Raw
	if err := bson.UnmarshalExtJSON(stateJSON, false, &stateDocument); err != nil {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: convert state to BSON: %w", err)
	}
	configurationHash, err := stateConfigurationHash(state)
	if err != nil {
		return MatchSnapshotDocument{}, fmt.Errorf("match snapshot: hash configuration: %w", err)
	}
	return MatchSnapshotDocument{
		MatchID:           matchID,
		SchemaVersion:     MatchSnapshotSchemaVersion,
		MatchVersion:      state.Version,
		Origin:            SnapshotOriginNative,
		ConfigurationHash: configurationHash,
		State:             stateDocument,
		CreatedAt:         updatedAt.UTC(),
		UpdatedAt:         updatedAt.UTC(),
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
	if d.Origin != SnapshotOriginNative {
		return matchengine.State{}, fmt.Errorf("match snapshot: unsupported origin %q", d.Origin)
	}
	if d.ConfigurationHash == "" {
		return matchengine.State{}, fmt.Errorf("match snapshot: configuration hash is required")
	}
	if d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
		return matchengine.State{}, fmt.Errorf("match snapshot: timestamps are required")
	}
	if d.UpdatedAt.Before(d.CreatedAt) {
		return matchengine.State{}, fmt.Errorf("match snapshot: updated timestamp precedes creation")
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
	if err := matchengine.ValidateState(state); err != nil {
		return matchengine.State{}, fmt.Errorf("match snapshot: invalid recovered state: %w", err)
	}
	configurationHash, err := stateConfigurationHash(state)
	if err != nil {
		return matchengine.State{}, fmt.Errorf("match snapshot: hash recovered configuration: %w", err)
	}
	if configurationHash != d.ConfigurationHash {
		return matchengine.State{}, fmt.Errorf("match snapshot: immutable configuration hash mismatch")
	}
	return state, nil
}

func stateConfigurationHash(state matchengine.State) (string, error) {
	type hashedPoolSlot struct {
		ID  string          `json:"id"`
		Mod matchengine.Mod `json:"mod"`
	}
	pool := make([]hashedPoolSlot, 0, len(state.PoolSlots))
	for _, slot := range state.PoolSlots {
		pool = append(pool, hashedPoolSlot{ID: slot.ID, Mod: slot.Mod})
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].ID < pool[j].ID })
	payload, err := json.Marshal(struct {
		FirstBan  matchengine.TeamSide                        `json:"firstBan"`
		FirstPick matchengine.TeamSide                        `json:"firstPick"`
		Pool      []hashedPoolSlot                            `json:"pool"`
		Rosters   map[matchengine.TeamSide]matchengine.Roster `json:"rosters"`
		Timers    matchengine.TimerConfiguration              `json:"timers"`
	}{
		FirstBan: state.FirstBan, FirstPick: state.FirstPick, Pool: pool,
		Rosters: state.Rosters, Timers: state.Timers,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:]), nil
}
