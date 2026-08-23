package persistence_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
)

func TestMongoIntegrationCommandStoreIsAtomicIdempotentAndOrdered(t *testing.T) {
	client, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store := persistence.NewCommandStore(client, db)
	if err := store.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	snapshots := persistence.NewSnapshotStore(db)
	matchID := bson.NewObjectID()
	ready := integrationReadyState(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	if err := snapshots.Create(ctx, matchID, ready, now); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	actor := matchcommand.AuthorizedActor{
		UserID: bson.NewObjectID(), OsuID: 9001, GlobalRoles: []string{"referee"},
		EngineActor: matchengine.RefereeActor(),
	}
	authorize := func(context.Context) (matchcommand.AuthorizedActor, error) { return actor, nil }
	startEnvelope := persistenceEnvelope(matchID, 0, "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409", "start-hash", "START_MATCH", now)
	start := func(state matchengine.State, authorized matchcommand.AuthorizedActor) (matchengine.Transition, error) {
		return matchengine.Execute(state, authorized.EngineActor, matchengine.StartMatch{}, now)
	}

	first, err := store.Apply(ctx, startEnvelope, authorize, start)
	if err != nil {
		t.Fatalf("Apply start: %v", err)
	}
	if first.Disposition != matchcommand.DispositionApplied || first.ResultingVersion != 1 {
		t.Fatalf("first result = %+v", first)
	}
	revokedAuthorize := func(context.Context) (matchcommand.AuthorizedActor, error) {
		return matchcommand.AuthorizedActor{}, errors.New("authorization changed after commit")
	}
	replayed, err := persistence.NewCommandStore(client, db).Apply(ctx, startEnvelope, revokedAuthorize, start)
	if err != nil {
		t.Fatalf("Apply replay after store recreation: %v", err)
	}
	if replayed.Disposition != matchcommand.DispositionReplayed || replayed.ResultingVersion != 1 {
		t.Fatalf("replay result = %+v", replayed)
	}
	if len(first.Events) == 0 || len(replayed.Events) != len(first.Events) || replayed.Events[0].EventID != first.Events[0].EventID || replayed.Events[0].Sequence != first.Events[0].Sequence || !replayed.Events[0].OccurredAt.Equal(first.Events[0].OccurredAt) {
		t.Fatalf("replay changed committed event identity: first=%+v replay=%+v", first.Events, replayed.Events)
	}
	assertCommandCollectionCount(t, ctx, db, persistence.MatchCommandReceiptsCollection, 1)
	assertCommandCollectionCount(t, ctx, db, persistence.MatchActionLogCollection, 1)
	assertCommandCollectionCount(t, ctx, db, persistence.MatchOutboxCollection, int64(len(first.Events)))
	if baseline, err := store.LatestEventSequenceAtVersion(ctx, matchID, 0); err != nil || baseline != 0 {
		t.Fatalf("version 0 event baseline=%d err=%v", baseline, err)
	}
	if baseline, err := store.LatestEventSequenceAtVersion(ctx, matchID, first.ResultingVersion); err != nil || baseline != uint64(len(first.Events)) {
		t.Fatalf("version %d event baseline=%d err=%v", first.ResultingVersion, baseline, err)
	}
	actions, err := store.ListActions(ctx, matchID, 50)
	if err != nil || len(actions) != 1 || actions[0].Actor.OsuID != actor.OsuID || actions[0].ResultingVersion != 1 {
		t.Fatalf("action log = %+v, err=%v", actions, err)
	}
	storedState, err := store.LoadStateAtVersion(ctx, matchID, first.ResultingVersion)
	if err != nil || storedState.Version != first.ResultingVersion || storedState.Lifecycle != first.State.Lifecycle {
		t.Fatalf("state at version=%+v err=%v", storedState, err)
	}

	mismatch := startEnvelope
	mismatch.RequestHash = "different-hash"
	if _, err := store.Apply(ctx, mismatch, authorize, start); matchcommand.ErrorOf(err) == nil ||
		matchcommand.ErrorOf(err).Code != matchcommand.CodeDuplicateCommandMismatch {
		t.Fatalf("duplicate mismatch error = %v", err)
	}

	loaded, err := snapshots.Load(ctx, matchID)
	if err != nil || loaded.Version != 1 {
		t.Fatalf("loaded version = %d, err=%v", loaded.Version, err)
	}
	var events []persistence.MatchOutboxDocument
	cursor, err := db.Collection(persistence.MatchOutboxCollection).Find(ctx, bson.M{"match_id": matchID})
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.All(ctx, &events); err != nil {
		t.Fatal(err)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) || event.Status != persistence.OutboxPending || event.ResultingVersion != 1 {
			t.Fatalf("outbox[%d] = %+v", index, event)
		}
	}
	unpublished, err := store.ListUnpublishedEvents(ctx, 100)
	if err != nil || len(unpublished) != 1 || unpublished[0].Sequence != 1 {
		t.Fatalf("earliest unpublished event=%+v err=%v", unpublished, err)
	}
	failedEventID := unpublished[0].EventID
	if err := store.MarkEventFailed(ctx, failedEventID, "injected publisher outage"); err != nil {
		t.Fatalf("MarkEventFailed: %v", err)
	}
	unpublished, err = store.ListUnpublishedEvents(ctx, 100)
	if err != nil || len(unpublished) != 0 {
		t.Fatalf("later event bypassed failed predecessor=%+v err=%v", unpublished, err)
	}
	failed, err := store.ListFailedEvents(ctx, matchID, 100)
	if err != nil || len(failed) != 1 || failed[0].EventID != failedEventID || failed[0].Attempts != 1 {
		t.Fatalf("visible failed event=%+v err=%v", failed, err)
	}
	if err := store.RetryFailedEvent(ctx, matchID, failedEventID); err != nil {
		t.Fatalf("RetryFailedEvent: %v", err)
	}
	unpublished, err = store.ListUnpublishedEvents(ctx, 100)
	if err != nil || len(unpublished) != 1 || unpublished[0].EventID != failedEventID {
		t.Fatalf("manually retried event queue=%+v err=%v", unpublished, err)
	}
	if err := store.MarkEventPublished(ctx, failedEventID, now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkEventPublished: %v", err)
	}
	unpublished, err = store.ListUnpublishedEvents(ctx, 100)
	if err != nil || len(unpublished) != 1 || unpublished[0].Sequence != 2 {
		t.Fatalf("next event after acknowledgement=%+v err=%v", unpublished, err)
	}
	loaded, err = snapshots.Load(ctx, matchID)
	if err != nil || loaded.Version != 1 {
		t.Fatalf("publisher status changed match snapshot: version=%d err=%v", loaded.Version, err)
	}

	beforeReceipts := commandCollectionCount(t, ctx, db, persistence.MatchCommandReceiptsCollection)
	rejectedEnvelope := persistenceEnvelope(matchID, 1, "018f4f2c-8f4f-7fd0-a55e-34a7f1a09410", "reject-hash", "PAUSE_TIMER", now.Add(time.Second))
	_, err = store.Apply(ctx, rejectedEnvelope, authorize, func(state matchengine.State, actor matchcommand.AuthorizedActor) (matchengine.Transition, error) {
		return matchengine.Transition{}, errors.New("injected engine failure")
	})
	if err == nil {
		t.Fatal("injected failure was accepted")
	}
	if got := commandCollectionCount(t, ctx, db, persistence.MatchCommandReceiptsCollection); got != beforeReceipts {
		t.Fatalf("failed command changed receipt count: %d -> %d", beforeReceipts, got)
	}
	loaded, _ = snapshots.Load(ctx, matchID)
	if loaded.Version != 1 {
		t.Fatalf("failed command changed snapshot to version %d", loaded.Version)
	}
}

func TestMongoIntegrationCommandStoreAllowsOneConcurrentVersion(t *testing.T) {
	client, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := persistence.NewCommandStore(client, db)
	if err := store.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	matchID := bson.NewObjectID()
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	ready := integrationReadyState(t)
	started := integrationExecute(t, ready, matchengine.RefereeActor(), matchengine.StartMatch{}, now)
	if err := persistence.NewSnapshotStore(db).Create(ctx, matchID, started, now); err != nil {
		t.Fatal(err)
	}
	actor := matchcommand.AuthorizedActor{UserID: bson.NewObjectID(), OsuID: 1001, EngineActor: matchengine.StrategistActor(started.ActiveTeam)}
	authorize := func(context.Context) (matchcommand.AuthorizedActor, error) { return actor, nil }

	type candidate struct {
		envelope matchcommand.Envelope
		slot     string
	}
	candidates := []candidate{
		{persistenceEnvelope(matchID, 1, "018f4f2c-8f4f-7fd0-a55e-34a7f1a09411", "ban-one", "BAN_POOL_SLOT", now.Add(time.Second)), "NM1"},
		{persistenceEnvelope(matchID, 1, "018f4f2c-8f4f-7fd0-a55e-34a7f1a09412", "ban-two", "BAN_POOL_SLOT", now.Add(time.Second)), "NM2"},
	}
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, candidate := range candidates {
		wait.Go(func() {
			_, err := store.Apply(ctx, candidate.envelope, authorize, func(state matchengine.State, authorized matchcommand.AuthorizedActor) (matchengine.Transition, error) {
				return matchengine.Execute(state, authorized.EngineActor, matchengine.BanPoolSlot{PoolSlotID: candidate.slot}, now.Add(time.Second))
			})
			results <- err
		})
	}
	wait.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		if commandErr := matchcommand.ErrorOf(err); commandErr != nil && commandErr.Code == matchcommand.CodeMatchVersionConflict {
			conflict++
			continue
		}
		t.Fatalf("unexpected concurrent error: %v", err)
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent success=%d conflict=%d", success, conflict)
	}
}

func persistenceEnvelope(matchID bson.ObjectID, version uint64, commandID, hash, commandType string, now time.Time) matchcommand.Envelope {
	hashBytes := sha256.Sum256([]byte(hash))
	return matchcommand.Envelope{
		MatchID: matchID, ExpectedVersion: version, CommandID: commandID,
		CommandType: commandType, RequestHash: fmt.Sprintf("%x", hashBytes[:]), PayloadJSON: []byte(`{}`), OccurredAt: now,
	}
}

func assertCommandCollectionCount(t *testing.T, ctx context.Context, db *mongo.Database, collection string, want int64) {
	t.Helper()
	if got := commandCollectionCount(t, ctx, db, collection); got != want {
		t.Fatalf("%s count = %d, want %d", collection, got, want)
	}
}

func commandCollectionCount(t *testing.T, ctx context.Context, db *mongo.Database, collection string) int64 {
	t.Helper()
	count, err := db.Collection(collection).CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("count %s: %v", collection, err)
	}
	return count
}
