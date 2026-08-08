package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rctHubBackend/internal/matchengine"
)

const (
	MatchSnapshotsCollection = "match_snapshots"
	legacyMatchesCollection  = "matches"
)

var (
	ErrSnapshotNotFound             = errors.New("authoritative match snapshot not found")
	ErrInvalidSnapshotIdentifier    = errors.New("invalid authoritative match snapshot identifier")
	ErrSnapshotAlreadyExists        = errors.New("authoritative match snapshot already exists")
	ErrSnapshotVersionConflict      = errors.New("authoritative match snapshot version conflict")
	ErrInvalidSnapshotTransition    = errors.New("invalid authoritative snapshot transition")
	ErrSnapshotCorrupt              = errors.New("authoritative match snapshot is corrupt")
	ErrSnapshotIncompatible         = errors.New("authoritative match snapshot schema is incompatible")
	ErrSnapshotValidatorMissing     = errors.New("authoritative match snapshot validator is missing")
	ErrSnapshotValidatorMismatch    = errors.New("authoritative match snapshot validator is incompatible")
	ErrLegacyMatchRequiresMigration = errors.New("legacy match requires verified migration")
)

// SnapshotVersionConflictError reports the version observed after a failed
// compare-and-swap while preserving errors.Is compatibility with
// ErrSnapshotVersionConflict.
type SnapshotVersionConflictError struct {
	Expected uint64
	Current  uint64
}

func (e *SnapshotVersionConflictError) Error() string {
	return fmt.Sprintf(
		"%s: expected version %d, current version %d",
		ErrSnapshotVersionConflict,
		e.Expected,
		e.Current,
	)
}

func (e *SnapshotVersionConflictError) Unwrap() error {
	return ErrSnapshotVersionConflict
}

// SnapshotStore persists the single authoritative MatchEngine aggregate for a
// match. It never synthesizes engine state from the incompatible legacy model.
type SnapshotStore struct {
	snapshots     *mongo.Collection
	legacyMatches *mongo.Collection
}

func NewSnapshotStore(db *mongo.Database) *SnapshotStore {
	return &SnapshotStore{
		snapshots:     db.Collection(MatchSnapshotsCollection),
		legacyMatches: db.Collection(legacyMatchesCollection),
	}
}

// EnsureIndexes creates indexes used by recovery and migration audits. MongoDB
// already guarantees one aggregate per match through the unique _id index.
func (s *SnapshotStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.snapshots.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "schema_version", Value: 1}, {Key: "updated_at", Value: -1}},
			Options: options.Index().SetName("snapshot_schema_updated_at"),
		},
		{
			Keys:    bson.D{{Key: "origin", Value: 1}, {Key: "updated_at", Value: -1}},
			Options: options.Index().SetName("snapshot_origin_updated_at"),
		},
	})
	if err != nil {
		return fmt.Errorf("ensure match snapshot indexes: %w", err)
	}
	return nil
}

// InstallValidator installs the database-level fail-closed contract for
// authoritative snapshots. It is intended for the privileged initdb process;
// application instances only verify the contract at startup.
func (s *SnapshotStore) InstallValidator(ctx context.Context) error {
	err := s.snapshots.Database().RunCommand(ctx, bson.D{
		{Key: "collMod", Value: MatchSnapshotsCollection},
		{Key: "validator", Value: MatchSnapshotValidator()},
		{Key: "validationLevel", Value: "strict"},
		{Key: "validationAction", Value: "error"},
	}).Err()
	if err != nil {
		return fmt.Errorf("install match snapshot validator: %w", err)
	}
	return nil
}

// VerifyValidator confirms that initdb installed the exact validator expected
// by this binary without requiring schema-management permissions at runtime.
func (s *SnapshotStore) VerifyValidator(ctx context.Context) error {
	cursor, err := s.snapshots.Database().ListCollections(ctx, bson.M{"name": MatchSnapshotsCollection})
	if err != nil {
		return fmt.Errorf("inspect match snapshot validator: %w", err)
	}
	defer cursor.Close(ctx)

	if !cursor.Next(ctx) {
		if err := cursor.Err(); err != nil {
			return fmt.Errorf("inspect match snapshot validator: %w", err)
		}
		return ErrSnapshotValidatorMissing
	}
	var collection struct {
		Options struct {
			Validator        bson.Raw `bson:"validator"`
			ValidationLevel  string   `bson:"validationLevel"`
			ValidationAction string   `bson:"validationAction"`
		} `bson:"options"`
	}
	if err := cursor.Decode(&collection); err != nil {
		return fmt.Errorf("decode match snapshot validator: %w", err)
	}
	if len(collection.Options.Validator) == 0 {
		return ErrSnapshotValidatorMissing
	}
	if collection.Options.ValidationLevel != "strict" || collection.Options.ValidationAction != "error" {
		return fmt.Errorf(
			"%w: validation level %q, action %q",
			ErrSnapshotValidatorMismatch,
			collection.Options.ValidationLevel,
			collection.Options.ValidationAction,
		)
	}

	actual, err := normalizeBSONDocument(collection.Options.Validator)
	if err != nil {
		return fmt.Errorf("decode installed match snapshot validator: %w", err)
	}
	expected, err := normalizeBSONDocument(MatchSnapshotValidator())
	if err != nil {
		return fmt.Errorf("encode expected match snapshot validator: %w", err)
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("%w: validator document differs from schema version %d", ErrSnapshotValidatorMismatch, MatchSnapshotSchemaVersion)
	}
	return nil
}

func normalizeBSONDocument(value any) (any, error) {
	var encoded []byte
	var err error
	if raw, ok := value.(bson.Raw); ok {
		encoded = raw
	} else {
		encoded, err = bson.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	extendedJSON, err := bson.MarshalExtJSON(bson.Raw(encoded), true, false)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(extendedJSON, &document); err != nil {
		return nil, err
	}
	return document, nil
}

func (s *SnapshotStore) Create(ctx context.Context, matchID bson.ObjectID, state matchengine.State, now time.Time) error {
	document, err := NewMatchSnapshotDocument(matchID, state, now)
	if err != nil {
		return fmt.Errorf("create authoritative snapshot: %w", err)
	}
	if _, err := s.snapshots.InsertOne(ctx, document); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrSnapshotAlreadyExists
		}
		return fmt.Errorf("insert authoritative snapshot: %w", err)
	}
	return nil
}

func (s *SnapshotStore) Load(ctx context.Context, matchID bson.ObjectID) (matchengine.State, error) {
	if matchID == bson.NilObjectID {
		return matchengine.State{}, fmt.Errorf("%w: match ID is required", ErrInvalidSnapshotIdentifier)
	}
	var document MatchSnapshotDocument
	err := s.snapshots.FindOne(ctx, bson.M{"_id": matchID}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return matchengine.State{}, s.missingSnapshotError(ctx, matchID)
	}
	if err != nil {
		return matchengine.State{}, fmt.Errorf("load authoritative snapshot: %w", err)
	}
	if document.SchemaVersion != MatchSnapshotSchemaVersion || document.Origin != SnapshotOriginNative {
		return matchengine.State{}, ErrSnapshotIncompatible
	}
	state, err := document.DecodeState()
	if err != nil {
		return matchengine.State{}, fmt.Errorf("%w: %v", ErrSnapshotCorrupt, err)
	}
	return state, nil
}

// LoadMany batch-loads authoritative states. Missing IDs are omitted so the
// caller can distinguish an absent snapshot without an N+1 fallback query.
func (s *SnapshotStore) LoadMany(ctx context.Context, matchIDs []bson.ObjectID) (map[bson.ObjectID]matchengine.State, error) {
	states := make(map[bson.ObjectID]matchengine.State, len(matchIDs))
	if len(matchIDs) == 0 {
		return states, nil
	}
	unique := make([]bson.ObjectID, 0, len(matchIDs))
	seen := make(map[bson.ObjectID]struct{}, len(matchIDs))
	for _, matchID := range matchIDs {
		if matchID == bson.NilObjectID {
			return nil, fmt.Errorf("%w: match ID is required", ErrInvalidSnapshotIdentifier)
		}
		if _, exists := seen[matchID]; exists {
			continue
		}
		seen[matchID] = struct{}{}
		unique = append(unique, matchID)
	}
	cursor, err := s.snapshots.Find(ctx, bson.M{"_id": bson.M{"$in": unique}})
	if err != nil {
		return nil, fmt.Errorf("load authoritative snapshots: %w", err)
	}
	defer cursor.Close(ctx)
	var documents []MatchSnapshotDocument
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode authoritative snapshots: %w", err)
	}
	for _, document := range documents {
		if document.SchemaVersion != MatchSnapshotSchemaVersion || document.Origin != SnapshotOriginNative {
			return nil, fmt.Errorf("match %s: %w", document.MatchID.Hex(), ErrSnapshotIncompatible)
		}
		state, decodeErr := document.DecodeState()
		if decodeErr != nil {
			return nil, fmt.Errorf("match %s: %w: %v", document.MatchID.Hex(), ErrSnapshotCorrupt, decodeErr)
		}
		states[document.MatchID] = state
	}
	return states, nil
}

// CompareAndSwap commits exactly one engine transition. The next state must be
// expectedVersion+1, matching MatchEngine.Execute's version contract.
func (s *SnapshotStore) CompareAndSwap(
	ctx context.Context,
	matchID bson.ObjectID,
	expectedVersion uint64,
	next matchengine.State,
	now time.Time,
) error {
	if expectedVersion == ^uint64(0) || next.Version != expectedVersion+1 {
		return fmt.Errorf(
			"%w: next version %d does not follow expected version %d",
			ErrInvalidSnapshotTransition,
			next.Version,
			expectedVersion,
		)
	}
	document, err := NewMatchSnapshotDocument(matchID, next, now)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSnapshotTransition, err)
	}
	result, err := s.snapshots.UpdateOne(
		ctx,
		bson.M{
			"_id":                matchID,
			"schema_version":     MatchSnapshotSchemaVersion,
			"origin":             SnapshotOriginNative,
			"configuration_hash": document.ConfigurationHash,
			"match_version":      expectedVersion,
			"updated_at":         bson.M{"$lte": document.UpdatedAt},
		},
		bson.M{"$set": bson.M{
			"schema_version": document.SchemaVersion,
			"match_version":  document.MatchVersion,
			"state":          document.State,
			"updated_at":     document.UpdatedAt,
		}},
	)
	if err != nil {
		return fmt.Errorf("compare-and-swap authoritative snapshot: %w", err)
	}
	if result.MatchedCount == 1 {
		return nil
	}

	var current MatchSnapshotDocument
	err = s.snapshots.FindOne(ctx, bson.M{"_id": matchID}).Decode(&current)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return s.missingSnapshotError(ctx, matchID)
	}
	if err != nil {
		return fmt.Errorf("resolve authoritative snapshot conflict: %w", err)
	}
	if current.SchemaVersion != MatchSnapshotSchemaVersion || current.Origin != SnapshotOriginNative {
		return ErrSnapshotIncompatible
	}
	if _, decodeErr := current.DecodeState(); decodeErr != nil {
		return fmt.Errorf("%w: %v", ErrSnapshotCorrupt, decodeErr)
	}
	if current.ConfigurationHash != document.ConfigurationHash {
		return fmt.Errorf("%w: immutable configuration changed", ErrInvalidSnapshotTransition)
	}
	if document.UpdatedAt.Before(current.UpdatedAt) {
		return fmt.Errorf("%w: snapshot timestamp moved backwards", ErrInvalidSnapshotTransition)
	}
	return &SnapshotVersionConflictError{
		Expected: expectedVersion,
		Current:  current.MatchVersion,
	}
}

func (s *SnapshotStore) missingSnapshotError(ctx context.Context, matchID bson.ObjectID) error {
	err := s.legacyMatches.FindOne(
		ctx,
		bson.M{"_id": matchID},
		options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Err()
	if err == nil {
		return ErrLegacyMatchRequiresMigration
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrSnapshotNotFound
	}
	return fmt.Errorf("check legacy match compatibility: %w", err)
}
