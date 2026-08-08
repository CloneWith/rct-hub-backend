package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
)

const (
	MatchCommandReceiptsCollection = "match_command_receipts"
	MatchActionLogCollection       = "match_action_log"
	MatchOutboxCollection          = "match_outbox"
)

var (
	ErrCommandValidatorMissing  = errors.New("match command collection validator is missing")
	ErrCommandValidatorMismatch = errors.New("match command collection validator is incompatible")
)

type OutboxStatus string

const (
	OutboxPending   OutboxStatus = "PENDING"
	OutboxPublished OutboxStatus = "PUBLISHED"
	OutboxFailed    OutboxStatus = "FAILED"
)

type CommandActorDocument struct {
	UserID          bson.ObjectID          `bson:"user_id"`
	OsuID           int64                  `bson:"osu_id"`
	GlobalRoles     []string               `bson:"global_roles"`
	Capability      matchengine.Capability `bson:"capability"`
	Team            *matchengine.TeamSide  `bson:"team,omitempty"`
	AdminOverride   bool                   `bson:"admin_override"`
	RefereeOverride bool                   `bson:"referee_override"`
}

type MatchCommandReceiptDocument struct {
	ID               bson.ObjectID        `bson:"_id"`
	MatchID          bson.ObjectID        `bson:"match_id"`
	CommandID        string               `bson:"command_id"`
	RequestHash      string               `bson:"request_hash"`
	CommandType      string               `bson:"command_type"`
	ExpectedVersion  uint64               `bson:"expected_version"`
	PreviousVersion  uint64               `bson:"previous_version"`
	ResultingVersion uint64               `bson:"resulting_version"`
	Actor            CommandActorDocument `bson:"actor"`
	StateJSON        []byte               `bson:"state_json"`
	EventsJSON       []byte               `bson:"events_json"`
	CreatedAt        time.Time            `bson:"created_at"`
}

type MatchActionDocument struct {
	ID               bson.ObjectID        `bson:"_id"`
	MatchID          bson.ObjectID        `bson:"match_id"`
	CommandID        string               `bson:"command_id"`
	CommandType      string               `bson:"command_type"`
	PreviousVersion  uint64               `bson:"previous_version"`
	ResultingVersion uint64               `bson:"resulting_version"`
	Actor            CommandActorDocument `bson:"actor"`
	Reason           string               `bson:"reason,omitempty"`
	CommandPayload   bson.Raw             `bson:"command_payload"`
	Events           []bson.Raw           `bson:"events"`
	CreatedAt        time.Time            `bson:"created_at"`
}

type MatchOutboxDocument struct {
	ID               bson.ObjectID         `bson:"_id"`
	EventID          string                `bson:"event_id"`
	MatchID          bson.ObjectID         `bson:"match_id"`
	Sequence         uint64                `bson:"sequence"`
	ResultingVersion uint64                `bson:"resulting_version"`
	Type             matchengine.EventType `bson:"type"`
	Actor            CommandActorDocument  `bson:"actor"`
	Payload          bson.Raw              `bson:"payload"`
	Status           OutboxStatus          `bson:"status"`
	Attempts         int                   `bson:"attempts"`
	LastError        string                `bson:"last_error,omitempty"`
	OccurredAt       time.Time             `bson:"occurred_at"`
	CreatedAt        time.Time             `bson:"created_at"`
	PublishedAt      *time.Time            `bson:"published_at,omitempty"`
}

type CommandStore struct {
	client    *mongo.Client
	snapshots *SnapshotStore
	receipts  *mongo.Collection
	actions   *mongo.Collection
	outbox    *mongo.Collection
}

func NewCommandStore(client *mongo.Client, db *mongo.Database) *CommandStore {
	return &CommandStore{
		client: client, snapshots: NewSnapshotStore(db),
		receipts: db.Collection(MatchCommandReceiptsCollection),
		actions:  db.Collection(MatchActionLogCollection),
		outbox:   db.Collection(MatchOutboxCollection),
	}
}

func (s *CommandStore) EnsureIndexes(ctx context.Context) error {
	if _, err := s.receipts.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_id", Value: 1}, {Key: "command_id", Value: 1}}, Options: options.Index().SetName("match_command_id_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "created_at", Value: 1}}, Options: options.Index().SetName("command_receipt_created_at")},
	}); err != nil {
		return fmt.Errorf("ensure command receipt indexes: %w", err)
	}
	if _, err := s.actions.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_id", Value: 1}, {Key: "resulting_version", Value: 1}}, Options: options.Index().SetName("match_action_version_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "actor.osu_id", Value: 1}, {Key: "created_at", Value: -1}}, Options: options.Index().SetName("match_action_actor_created_at")},
	}); err != nil {
		return fmt.Errorf("ensure match action indexes: %w", err)
	}
	if _, err := s.outbox.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "match_id", Value: 1}, {Key: "sequence", Value: 1}}, Options: options.Index().SetName("match_event_sequence_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "event_id", Value: 1}}, Options: options.Index().SetName("match_event_id_unique").SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "created_at", Value: 1}}, Options: options.Index().SetName("outbox_status_created_at")},
	}); err != nil {
		return fmt.Errorf("ensure match outbox indexes: %w", err)
	}
	return nil
}

func (s *CommandStore) InstallValidators(ctx context.Context) error {
	validators := []struct {
		collection string
		validator  bson.M
	}{
		{MatchCommandReceiptsCollection, MatchCommandReceiptValidator()},
		{MatchActionLogCollection, MatchActionLogValidator()},
		{MatchOutboxCollection, MatchOutboxValidator()},
	}
	db := s.receipts.Database()
	for _, item := range validators {
		if err := ensureCommandCollection(ctx, db, item.collection); err != nil {
			return err
		}
		if err := s.receipts.Database().RunCommand(ctx, bson.D{
			{Key: "collMod", Value: item.collection},
			{Key: "validator", Value: item.validator},
			{Key: "validationLevel", Value: "strict"},
			{Key: "validationAction", Value: "error"},
		}).Err(); err != nil {
			return fmt.Errorf("install %s validator: %w", item.collection, err)
		}
	}
	return nil
}

func ensureCommandCollection(ctx context.Context, db *mongo.Database, name string) error {
	if err := db.CreateCollection(ctx, name); err != nil {
		var commandErr mongo.CommandError
		if errors.As(err, &commandErr) && commandErr.Code == 48 {
			return nil
		}
		return fmt.Errorf("create %s collection: %w", name, err)
	}
	return nil
}

func (s *CommandStore) VerifyValidators(ctx context.Context) error {
	validators := []struct {
		collection string
		validator  bson.M
	}{
		{MatchCommandReceiptsCollection, MatchCommandReceiptValidator()},
		{MatchActionLogCollection, MatchActionLogValidator()},
		{MatchOutboxCollection, MatchOutboxValidator()},
	}
	for _, item := range validators {
		if err := verifyCollectionValidator(ctx, s.receipts.Database(), item.collection, item.validator); err != nil {
			return err
		}
	}
	return nil
}

func verifyCollectionValidator(ctx context.Context, db *mongo.Database, collection string, expected bson.M) error {
	cursor, err := db.ListCollections(ctx, bson.M{"name": collection})
	if err != nil {
		return fmt.Errorf("inspect %s validator: %w", collection, err)
	}
	defer cursor.Close(ctx)
	if !cursor.Next(ctx) {
		if err := cursor.Err(); err != nil {
			return fmt.Errorf("inspect %s validator: %w", collection, err)
		}
		return fmt.Errorf("%w: %s", ErrCommandValidatorMissing, collection)
	}
	var metadata struct {
		Options struct {
			Validator        bson.Raw `bson:"validator"`
			ValidationLevel  string   `bson:"validationLevel"`
			ValidationAction string   `bson:"validationAction"`
		} `bson:"options"`
	}
	if err := cursor.Decode(&metadata); err != nil {
		return fmt.Errorf("decode %s validator: %w", collection, err)
	}
	if len(metadata.Options.Validator) == 0 {
		return fmt.Errorf("%w: %s", ErrCommandValidatorMissing, collection)
	}
	actualDocument, err := normalizeBSONDocument(metadata.Options.Validator)
	if err != nil {
		return fmt.Errorf("decode installed %s validator: %w", collection, err)
	}
	expectedDocument, err := normalizeBSONDocument(expected)
	if err != nil {
		return fmt.Errorf("encode expected %s validator: %w", collection, err)
	}
	actualJSON, err := json.Marshal(actualDocument)
	if err != nil {
		return fmt.Errorf("encode installed %s validator: %w", collection, err)
	}
	expectedJSON, err := json.Marshal(expectedDocument)
	if err != nil {
		return fmt.Errorf("encode expected %s validator: %w", collection, err)
	}
	if metadata.Options.ValidationLevel != "strict" || metadata.Options.ValidationAction != "error" || string(actualJSON) != string(expectedJSON) {
		return fmt.Errorf("%w: %s", ErrCommandValidatorMismatch, collection)
	}
	return nil
}

// ListUnpublishedEvents returns durable events in a deterministic order for a
// future publisher. Ordering is guaranteed within each match by Sequence.
func (s *CommandStore) ListUnpublishedEvents(ctx context.Context, limit int64) ([]MatchOutboxDocument, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	cursor, err := s.outbox.Find(
		ctx,
		bson.M{"status": bson.M{"$in": []OutboxStatus{OutboxPending, OutboxFailed}}},
		options.Find().SetSort(bson.D{{Key: "match_id", Value: 1}, {Key: "sequence", Value: 1}}).SetLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("list unpublished match events: %w", err)
	}
	defer cursor.Close(ctx)
	var events []MatchOutboxDocument
	if err := cursor.All(ctx, &events); err != nil {
		return nil, fmt.Errorf("decode unpublished match events: %w", err)
	}
	return events, nil
}

func (s *CommandStore) MarkEventPublished(ctx context.Context, eventID string, publishedAt time.Time) error {
	if eventID == "" || publishedAt.IsZero() {
		return fmt.Errorf("event ID and publication time are required")
	}
	result, err := s.outbox.UpdateOne(ctx,
		bson.M{"event_id": eventID, "status": bson.M{"$in": []OutboxStatus{OutboxPending, OutboxFailed}}},
		bson.M{"$set": bson.M{"status": OutboxPublished, "published_at": publishedAt.UTC(), "last_error": ""}, "$inc": bson.M{"attempts": 1}},
	)
	if err != nil {
		return fmt.Errorf("mark match event published: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("unpublished event %q was not found", eventID)
	}
	return nil
}

func (s *CommandStore) MarkEventFailed(ctx context.Context, eventID, message string) error {
	if eventID == "" || message == "" {
		return fmt.Errorf("event ID and failure message are required")
	}
	result, err := s.outbox.UpdateOne(ctx,
		bson.M{"event_id": eventID, "status": bson.M{"$ne": OutboxPublished}},
		bson.M{"$set": bson.M{"status": OutboxFailed, "last_error": message}, "$inc": bson.M{"attempts": 1}},
	)
	if err != nil {
		return fmt.Errorf("mark match event failed: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("unpublished event %q was not found", eventID)
	}
	return nil
}

// ListActions returns the newest durable audit entries for one match.
func (s *CommandStore) ListActions(ctx context.Context, matchID bson.ObjectID, limit int) ([]MatchActionDocument, error) {
	if matchID == bson.NilObjectID {
		return nil, fmt.Errorf("match ID is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	cursor, err := s.actions.Find(ctx, bson.M{"match_id": matchID}, options.Find().SetSort(bson.D{{Key: "resulting_version", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("list match actions: %w", err)
	}
	defer cursor.Close(ctx)
	var actions []MatchActionDocument
	if err := cursor.All(ctx, &actions); err != nil {
		return nil, fmt.Errorf("decode match actions: %w", err)
	}
	if actions == nil {
		actions = []MatchActionDocument{}
	}
	return actions, nil
}

func (s *CommandStore) Apply(
	ctx context.Context,
	envelope matchcommand.Envelope,
	authorize matchcommand.AuthorizeFunc,
	execute matchcommand.ExecuteFunc,
) (matchcommand.Result, error) {
	if s == nil || s.client == nil || s.snapshots == nil || authorize == nil || execute == nil {
		return matchcommand.Result{}, matchcommand.NewError(matchcommand.CodeInternalError, "command store is not configured", nil)
	}
	if replayed, found, replayErr := s.loadReceipt(ctx, envelope); replayErr != nil {
		return matchcommand.Result{}, replayErr
	} else if found {
		return replayed, nil
	}
	session, err := s.client.StartSession()
	if err != nil {
		return matchcommand.Result{}, matchcommand.NewError(matchcommand.CodeInternalError, "start command transaction", err)
	}
	defer session.EndSession(ctx)

	var result matchcommand.Result
	_, err = session.WithTransaction(ctx, func(txCtx context.Context) (any, error) {
		result = matchcommand.Result{}
		replayed, found, receiptErr := s.loadReceipt(txCtx, envelope)
		if receiptErr != nil {
			return nil, receiptErr
		}
		if found {
			result = replayed
			return nil, nil
		}
		actor, authErr := authorize(txCtx)
		if authErr != nil {
			return nil, authErr
		}

		current, loadErr := s.snapshots.Load(txCtx, envelope.MatchID)
		if loadErr != nil {
			return nil, mapSnapshotCommandError(loadErr)
		}
		if current.Version != envelope.ExpectedVersion {
			return nil, matchcommand.VersionConflict(envelope.ExpectedVersion, current.Version)
		}
		transition, executeErr := execute(current.Clone(), actor)
		if executeErr != nil {
			return nil, executeErr
		}
		if transition.State.Version != current.Version+1 {
			return nil, matchcommand.NewError(matchcommand.CodeInternalError, "engine returned a non-monotonic version", nil)
		}

		actorDocument := commandActorDocument(actor)
		payload, encodeErr := jsonDocument(envelope.PayloadJSON)
		if encodeErr != nil {
			return nil, matchcommand.NewError(matchcommand.CodeInvalidRequest, "command payload is not a JSON object", encodeErr)
		}
		eventDocuments, eventPayloads, committedEvents, encodeErr := s.buildOutboxDocuments(
			txCtx, envelope, actorDocument, transition,
		)
		if encodeErr != nil {
			return nil, encodeErr
		}
		stateJSON, encodeErr := json.Marshal(transition.State)
		if encodeErr != nil {
			return nil, matchcommand.NewError(matchcommand.CodeInternalError, "encode command result state", encodeErr)
		}
		eventsJSON, encodeErr := json.Marshal(committedEvents)
		if encodeErr != nil {
			return nil, matchcommand.NewError(matchcommand.CodeInternalError, "encode command result events", encodeErr)
		}

		if swapErr := s.snapshots.CompareAndSwap(
			txCtx, envelope.MatchID, current.Version, transition.State, envelope.OccurredAt,
		); swapErr != nil {
			return nil, mapSnapshotCommandError(swapErr)
		}
		action := MatchActionDocument{
			ID: bson.NewObjectID(), MatchID: envelope.MatchID, CommandID: envelope.CommandID,
			CommandType: envelope.CommandType, PreviousVersion: current.Version,
			ResultingVersion: transition.State.Version, Actor: actorDocument,
			Reason: actor.Reason, CommandPayload: payload, Events: eventPayloads,
			CreatedAt: envelope.OccurredAt,
		}
		if _, insertErr := s.actions.InsertOne(txCtx, action); insertErr != nil {
			return nil, insertErr
		}
		if len(eventDocuments) > 0 {
			outboxWrites := make([]any, len(eventDocuments))
			for index := range eventDocuments {
				outboxWrites[index] = eventDocuments[index]
			}
			if _, insertErr := s.outbox.InsertMany(txCtx, outboxWrites); insertErr != nil {
				return nil, insertErr
			}
		}
		receipt := MatchCommandReceiptDocument{
			ID: bson.NewObjectID(), MatchID: envelope.MatchID, CommandID: envelope.CommandID,
			RequestHash: envelope.RequestHash, CommandType: envelope.CommandType,
			ExpectedVersion: envelope.ExpectedVersion, PreviousVersion: current.Version,
			ResultingVersion: transition.State.Version, Actor: actorDocument,
			StateJSON: stateJSON, EventsJSON: eventsJSON, CreatedAt: envelope.OccurredAt,
		}
		if _, insertErr := s.receipts.InsertOne(txCtx, receipt); insertErr != nil {
			return nil, insertErr
		}

		result = matchcommand.Result{
			CommandID: envelope.CommandID, Disposition: matchcommand.DispositionApplied,
			PreviousVersion: current.Version, ResultingVersion: transition.State.Version,
			State: transition.State.Clone(), Events: cloneCommittedEvents(committedEvents),
		}
		return nil, nil
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			if replayed, found, replayErr := s.loadReceipt(ctx, envelope); replayErr != nil {
				return matchcommand.Result{}, replayErr
			} else if found {
				return replayed, nil
			}
		}
		return matchcommand.Result{}, err
	}
	return result, nil
}

func (s *CommandStore) loadReceipt(
	ctx context.Context,
	envelope matchcommand.Envelope,
) (matchcommand.Result, bool, error) {
	var receipt MatchCommandReceiptDocument
	err := s.receipts.FindOne(ctx, bson.M{"match_id": envelope.MatchID, "command_id": envelope.CommandID}).Decode(&receipt)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return matchcommand.Result{}, false, nil
	}
	if err != nil {
		return matchcommand.Result{}, false, err
	}
	if receipt.RequestHash != envelope.RequestHash {
		return matchcommand.Result{}, true, matchcommand.NewError(
			matchcommand.CodeDuplicateCommandMismatch,
			"commandId was already used for a different request",
			nil,
		)
	}
	var state matchengine.State
	if err := json.Unmarshal(receipt.StateJSON, &state); err != nil {
		return matchcommand.Result{}, true, matchcommand.NewError(matchcommand.CodeInternalError, "decode stored command result state", err)
	}
	if err := matchengine.ValidateState(state); err != nil {
		return matchcommand.Result{}, true, matchcommand.NewError(matchcommand.CodeInternalError, "stored command result state is corrupt", err)
	}
	var events []matchcommand.CommittedEvent
	if err := json.Unmarshal(receipt.EventsJSON, &events); err != nil {
		return matchcommand.Result{}, true, matchcommand.NewError(matchcommand.CodeInternalError, "decode stored command result events", err)
	}
	for index := range events {
		if events[index].Actor.OsuID != 0 {
			continue
		}
		events[index].Actor = matchcommand.EventActor{
			OsuID: receipt.Actor.OsuID, Capability: receipt.Actor.Capability, Team: receipt.Actor.Team,
			AdminOverride: receipt.Actor.AdminOverride, RefereeOverride: receipt.Actor.RefereeOverride,
		}
	}
	return matchcommand.Result{
		CommandID: receipt.CommandID, Disposition: matchcommand.DispositionReplayed,
		PreviousVersion: receipt.PreviousVersion, ResultingVersion: receipt.ResultingVersion,
		State: state, Events: events,
	}, true, nil
}

func (s *CommandStore) buildOutboxDocuments(
	ctx context.Context,
	envelope matchcommand.Envelope,
	actor CommandActorDocument,
	transition matchengine.Transition,
) ([]MatchOutboxDocument, []bson.Raw, []matchcommand.CommittedEvent, error) {
	lastSequence, err := s.lastSequence(ctx, envelope.MatchID)
	if err != nil {
		return nil, nil, nil, err
	}
	documents := make([]MatchOutboxDocument, 0, len(transition.Events))
	payloads := make([]bson.Raw, 0, len(transition.Events))
	committedEvents := make([]matchcommand.CommittedEvent, 0, len(transition.Events))
	for index, event := range transition.Events {
		encoded, encodeErr := json.Marshal(event)
		if encodeErr != nil {
			return nil, nil, nil, matchcommand.NewError(matchcommand.CodeInternalError, "encode engine event", encodeErr)
		}
		payload, encodeErr := jsonDocument(encoded)
		if encodeErr != nil {
			return nil, nil, nil, matchcommand.NewError(matchcommand.CodeInternalError, "convert engine event to BSON", encodeErr)
		}
		eventID := uuid.NewString()
		sequence := lastSequence + uint64(index) + 1
		documents = append(documents, MatchOutboxDocument{
			ID: bson.NewObjectID(), EventID: eventID, MatchID: envelope.MatchID,
			Sequence:         sequence,
			ResultingVersion: transition.State.Version, Type: event.Type,
			Actor: actor, Payload: payload, Status: OutboxPending, Attempts: 0,
			OccurredAt: envelope.OccurredAt, CreatedAt: envelope.OccurredAt,
		})
		committed := matchcommand.CommittedEvent{
			EventID: eventID, Sequence: sequence, ResultingVersion: transition.State.Version,
			Type: event.Type, OccurredAt: envelope.OccurredAt,
			Actor: matchcommand.EventActor{
				OsuID: actor.OsuID, Capability: actor.Capability, Team: actor.Team,
				AdminOverride: actor.AdminOverride, RefereeOverride: actor.RefereeOverride,
			},
			Payload: event,
		}
		committedEvents = append(committedEvents, committed)
		auditEncoded, encodeErr := json.Marshal(committed)
		if encodeErr != nil {
			return nil, nil, nil, matchcommand.NewError(matchcommand.CodeInternalError, "encode committed event", encodeErr)
		}
		auditPayload, encodeErr := jsonDocument(auditEncoded)
		if encodeErr != nil {
			return nil, nil, nil, matchcommand.NewError(matchcommand.CodeInternalError, "convert committed event to BSON", encodeErr)
		}
		payloads = append(payloads, auditPayload)
	}
	return documents, payloads, committedEvents, nil
}

func (s *CommandStore) lastSequence(ctx context.Context, matchID bson.ObjectID) (uint64, error) {
	var latest struct {
		Sequence uint64 `bson:"sequence"`
	}
	err := s.outbox.FindOne(
		ctx,
		bson.M{"match_id": matchID},
		options.FindOne().SetSort(bson.D{{Key: "sequence", Value: -1}}).SetProjection(bson.M{"sequence": 1}),
	).Decode(&latest)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return latest.Sequence, nil
}

func commandActorDocument(actor matchcommand.AuthorizedActor) CommandActorDocument {
	var team *matchengine.TeamSide
	if actor.EngineActor.Team != nil {
		value := *actor.EngineActor.Team
		team = &value
	}
	return CommandActorDocument{
		UserID: actor.UserID, OsuID: actor.OsuID,
		GlobalRoles: append([]string(nil), actor.GlobalRoles...),
		Capability:  actor.EngineActor.Capability, Team: team,
		AdminOverride: actor.AdminOverride, RefereeOverride: actor.RefereeOverride,
	}
}

func jsonDocument(encoded []byte) (bson.Raw, error) {
	var document bson.Raw
	if err := bson.UnmarshalExtJSON(encoded, false, &document); err != nil {
		return nil, err
	}
	return document, nil
}

func cloneCommittedEvents(events []matchcommand.CommittedEvent) []matchcommand.CommittedEvent {
	encoded, err := json.Marshal(events)
	if err != nil {
		return nil
	}
	var clone []matchcommand.CommittedEvent
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil
	}
	return clone
}

func mapSnapshotCommandError(err error) error {
	var conflict *SnapshotVersionConflictError
	switch {
	case errors.As(err, &conflict):
		return matchcommand.VersionConflict(conflict.Expected, conflict.Current)
	case errors.Is(err, ErrSnapshotNotFound), errors.Is(err, ErrLegacyMatchRequiresMigration):
		return matchcommand.NewError(matchcommand.CodeResourceNotFound, "authoritative formal match snapshot was not found", err)
	case errors.Is(err, ErrSnapshotCorrupt), errors.Is(err, ErrSnapshotIncompatible), errors.Is(err, ErrInvalidSnapshotTransition):
		return matchcommand.NewError(matchcommand.CodeInternalError, "authoritative formal match snapshot is invalid", err)
	default:
		return matchcommand.NewError(matchcommand.CodeInternalError, "persist authoritative match command", err)
	}
}
