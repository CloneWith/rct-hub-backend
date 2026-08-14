package persistence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rctHubBackend/internal/database"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/internal/repository"
	"rctHubBackend/internal/service"
)

const integrationMongoEnv = "MONGODB_TEST_URI"
const integrationMongoInitReplicaEnv = "MONGODB_TEST_INIT_REPLICA"

func TestReplicaSetHostFromURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		want    string
		wantErr bool
	}{
		{name: "explicit port", uri: "mongodb://localhost:27018/?directConnection=true", want: "localhost:27018"},
		{name: "default port", uri: "mongodb://localhost/test", want: "localhost:27017"},
		{name: "credentials", uri: "mongodb://user:secret@mongo.internal:27019/test", want: "mongo.internal:27019"},
		{name: "multiple hosts", uri: "mongodb://mongo-a:27017,mongo-b:27017/test", wantErr: true},
		{name: "srv discovery", uri: "mongodb+srv://mongo.example/test", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := replicaSetHostFromURI(test.uri)
			if test.wantErr {
				if err == nil {
					t.Fatalf("replicaSetHostFromURI(%q) = %q, want error", test.uri, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("replicaSetHostFromURI(%q) = %q, %v; want %q", test.uri, got, err, test.want)
			}
		})
	}
}

func TestMongoIntegrationSnapshotStoreCASAndCompatibility(t *testing.T) {
	client, db := integrationMongo(t)
	_ = client
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	store := persistence.NewSnapshotStore(db)
	if err := store.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	assertSnapshotIndexes(t, ctx, db)

	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	ready := integrationReadyState(t)
	matchID := bson.NewObjectID()
	if err := store.Create(ctx, matchID, ready, now); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, matchID, ready, now); !errors.Is(err, persistence.ErrSnapshotAlreadyExists) {
		t.Fatalf("duplicate Create error = %v", err)
	}

	started := integrationExecute(t, ready, matchengine.RefereeActor(), matchengine.StartMatch{}, now.Add(time.Second))
	if err := store.CompareAndSwap(ctx, matchID, ready.Version, started, now.Add(time.Second)); err != nil {
		t.Fatalf("CompareAndSwap start: %v", err)
	}
	loaded, err := store.Load(ctx, matchID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertJSONEqual(t, loaded, started)

	next := integrationExecute(t, started, matchengine.StrategistActor(started.ActiveTeam), matchengine.BanPoolSlot{PoolSlotID: "NM1"}, now.Add(2*time.Second))
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- store.CompareAndSwap(ctx, matchID, started.Version, next, now.Add(2*time.Second))
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, persistence.ErrSnapshotVersionConflict):
			assertVersionConflict(t, result, started.Version, next.Version)
			conflicts++
		default:
			t.Fatalf("concurrent CAS error = %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent CAS = %d successes, %d conflicts", successes, conflicts)
	}
	staleErr := store.CompareAndSwap(ctx, matchID, 0, started, now.Add(3*time.Second))
	assertVersionConflict(t, staleErr, 0, next.Version)
	if err := store.CompareAndSwap(ctx, matchID, next.Version, next, now.Add(3*time.Second)); !errors.Is(err, persistence.ErrInvalidSnapshotTransition) {
		t.Fatalf("non-incrementing CAS error = %v", err)
	}
	tamperedConfiguration := next.Clone()
	tamperedConfiguration.Version++
	tamperedConfiguration.FirstPick = matchengine.TeamRed
	if err := store.CompareAndSwap(ctx, matchID, next.Version, tamperedConfiguration, now.Add(3*time.Second)); !errors.Is(err, persistence.ErrInvalidSnapshotTransition) {
		t.Fatalf("configuration-changing CAS error = %v", err)
	}
	afterNext := integrationExecute(t, next, matchengine.StrategistActor(next.ActiveTeam), matchengine.BanPoolSlot{PoolSlotID: "NM2"}, now.Add(3*time.Second))
	if err := store.CompareAndSwap(ctx, matchID, next.Version, afterNext, now); !errors.Is(err, persistence.ErrInvalidSnapshotTransition) {
		t.Fatalf("backwards-timestamp CAS error = %v", err)
	}

	legacyID := bson.NewObjectID()
	if _, err := db.Collection("matches").InsertOne(ctx, bson.M{"_id": legacyID, "status": "active"}); err != nil {
		t.Fatalf("insert legacy match: %v", err)
	}
	if _, err := store.Load(ctx, legacyID); !errors.Is(err, persistence.ErrLegacyMatchRequiresMigration) {
		t.Fatalf("legacy-only Load error = %v", err)
	}
	if _, err := store.Load(ctx, bson.NewObjectID()); !errors.Is(err, persistence.ErrSnapshotNotFound) {
		t.Fatalf("missing Load error = %v", err)
	}

	corruptID := bson.NewObjectID()
	if _, err := db.Collection(persistence.MatchSnapshotsCollection).InsertOne(ctx, bson.M{
		"_id": corruptID, "schema_version": persistence.MatchSnapshotSchemaVersion,
		"match_version": int64(0), "origin": persistence.SnapshotOriginNative,
		"state": bson.M{"version": int64(0)}, "created_at": now, "updated_at": now,
	}); err != nil {
		t.Fatalf("insert corrupt snapshot: %v", err)
	}
	if _, err := store.Load(ctx, corruptID); !errors.Is(err, persistence.ErrSnapshotCorrupt) {
		t.Fatalf("corrupt Load error = %v", err)
	}

	incompatibleID := bson.NewObjectID()
	incompatibleDocument, err := persistence.NewMatchSnapshotDocument(incompatibleID, ready, now)
	if err != nil {
		t.Fatal(err)
	}
	incompatibleDocument.SchemaVersion--
	if _, err := db.Collection(persistence.MatchSnapshotsCollection).InsertOne(ctx, incompatibleDocument); err != nil {
		t.Fatalf("insert incompatible snapshot: %v", err)
	}
	if _, err := store.Load(ctx, incompatibleID); !errors.Is(err, persistence.ErrSnapshotIncompatible) {
		t.Fatalf("incompatible Load error = %v", err)
	}

	runtimeDB := &database.DB{Mongo: db.Client(), MongoDB: db}
	if err := runtimeDB.VerifySchema(ctx); !errors.Is(err, persistence.ErrSnapshotValidatorMissing) {
		t.Fatalf("VerifySchema before initdb error = %v, want missing validator", err)
	}
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatalf("InstallValidator: %v", err)
	}
	commandStore := persistence.NewCommandStore(db.Client(), db)
	if err := commandStore.InstallValidators(ctx); err != nil {
		t.Fatalf("Install command validators: %v", err)
	}
	ircJobs := persistence.NewIRCJobStore(db)
	ircObservations := persistence.NewIRCObservationStore(db)
	beatmapMetadata := persistence.NewBeatmapMetadataStore(db)
	if err := ircJobs.InstallValidator(ctx); err != nil {
		t.Fatalf("Install IRC job validator: %v", err)
	}
	if err := ircObservations.InstallValidator(ctx); err != nil {
		t.Fatalf("Install IRC observation validator: %v", err)
	}
	if err := beatmapMetadata.InstallValidator(ctx); err != nil {
		t.Fatalf("Install beatmap metadata validator: %v", err)
	}
	if err := runtimeDB.VerifySchema(ctx); err != nil {
		t.Fatalf("VerifySchema after initdb: %v", err)
	}
	if err := store.InstallValidator(ctx); err != nil {
		t.Fatalf("idempotent InstallValidator: %v", err)
	}
	if err := commandStore.InstallValidators(ctx); err != nil {
		t.Fatalf("idempotent command validators: %v", err)
	}
	if err := ircJobs.InstallValidator(ctx); err != nil {
		t.Fatalf("idempotent IRC job validator: %v", err)
	}
	if err := ircObservations.InstallValidator(ctx); err != nil {
		t.Fatalf("idempotent IRC observation validator: %v", err)
	}
	if err := beatmapMetadata.InstallValidator(ctx); err != nil {
		t.Fatalf("idempotent beatmap metadata validator: %v", err)
	}
	if err := runtimeDB.VerifySchema(ctx); err != nil {
		t.Fatalf("VerifySchema after repeated initdb: %v", err)
	}
	if _, err := db.Collection(persistence.MatchSnapshotsCollection).InsertOne(ctx, bson.M{
		"_id": bson.NewObjectID(), "schema_version": persistence.MatchSnapshotSchemaVersion,
	}); err == nil {
		t.Fatal("strict snapshot validator accepted an incomplete document")
	}
	if err := db.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: persistence.MatchSnapshotsCollection},
		{Key: "validationLevel", Value: "moderate"},
	}).Err(); err != nil {
		t.Fatalf("drift snapshot validator: %v", err)
	}
	if err := runtimeDB.VerifySchema(ctx); !errors.Is(err, persistence.ErrSnapshotValidatorMismatch) {
		t.Fatalf("VerifySchema after drift error = %v, want validator mismatch", err)
	}
}

func TestMongoIntegrationLifecycleRecoveryPreservesBehavior(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := persistence.NewSnapshotStore(db)
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)

	for _, fixture := range integrationLifecycleFixtures(t, now) {
		t.Run(fixture.name, func(t *testing.T) {
			matchID := bson.NewObjectID()
			if err := store.Create(ctx, matchID, fixture.state, now); err != nil {
				t.Fatalf("Create: %v", err)
			}
			recovered, err := store.Load(ctx, matchID)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			assertJSONEqual(t, recovered, fixture.state)
			assertJSONEqual(t, matchengine.Analyze(recovered), matchengine.Analyze(fixture.state))

			wantTransition, wantErr := matchengine.Execute(fixture.state, fixture.actor, fixture.command, fixture.at)
			gotTransition, gotErr := matchengine.Execute(recovered, fixture.actor, fixture.command, fixture.at)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("Execute errors differ: recovered=%v original=%v", gotErr, wantErr)
			}
			assertJSONEqual(t, gotTransition, wantTransition)
		})
	}
}

func TestMongoIntegrationFormalBootstrapIsAtomic(t *testing.T) {
	client, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store := persistence.NewSnapshotStore(db)
	bootstrap := persistence.NewFormalMatchBootstrapStore(client, db)
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	room := integrationFormalRoom()
	if _, err := db.Collection("rooms").InsertOne(ctx, room); err != nil {
		t.Fatalf("insert room: %v", err)
	}
	seed, err := service.BuildFormalMatchSeed(room, now)
	if err != nil {
		t.Fatalf("BuildFormalMatchSeed: %v", err)
	}
	if err := bootstrap.Create(ctx, room.ID, seed.LegacyMatch, seed.State, now); err != nil {
		t.Fatalf("bootstrap Create: %v", err)
	}

	var persistedRoom domain.Room
	if err := db.Collection("rooms").FindOne(ctx, bson.M{"_id": room.ID}).Decode(&persistedRoom); err != nil {
		t.Fatalf("load room: %v", err)
	}
	if persistedRoom.MatchID == nil || *persistedRoom.MatchID != seed.LegacyMatch.ID {
		t.Fatalf("room match id = %v, want %s", persistedRoom.MatchID, seed.LegacyMatch.ID)
	}
	if count, err := db.Collection("matches").CountDocuments(ctx, bson.M{"_id": seed.LegacyMatch.ID}); err != nil || count != 1 {
		t.Fatalf("legacy shell count = %d, err %v", count, err)
	}
	recovered, err := store.Load(ctx, seed.LegacyMatch.ID)
	if err != nil {
		t.Fatalf("load authoritative snapshot: %v", err)
	}
	assertJSONEqual(t, recovered, seed.State)

	rollbackRoom := integrationFormalRoom()
	rollbackRoom.Code = "ROLLBACK"
	if _, err := db.Collection("rooms").InsertOne(ctx, rollbackRoom); err != nil {
		t.Fatalf("insert rollback room: %v", err)
	}
	rollbackSeed, err := service.BuildFormalMatchSeed(rollbackRoom, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, rollbackSeed.LegacyMatch.ID, rollbackSeed.State, now); err != nil {
		t.Fatalf("precreate conflicting snapshot: %v", err)
	}
	if err := bootstrap.Create(ctx, rollbackRoom.ID, rollbackSeed.LegacyMatch, rollbackSeed.State, now); !errors.Is(err, persistence.ErrFormalMatchAlreadyStarted) {
		t.Fatalf("conflicting bootstrap error = %v", err)
	}
	var rolledBackRoom domain.Room
	if err := db.Collection("rooms").FindOne(ctx, bson.M{"_id": rollbackRoom.ID}).Decode(&rolledBackRoom); err != nil {
		t.Fatal(err)
	}
	if rolledBackRoom.MatchID != nil {
		t.Fatalf("transaction left room.match_id = %v", rolledBackRoom.MatchID)
	}
	if count, err := db.Collection("matches").CountDocuments(ctx, bson.M{"_id": rollbackSeed.LegacyMatch.ID}); err != nil || count != 0 {
		t.Fatalf("transaction left legacy shell count = %d, err %v", count, err)
	}
}

func TestMongoIntegrationFormalBootstrapConcurrentStartHasOneWinner(t *testing.T) {
	client, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	room := integrationFormalRoom()
	if _, err := db.Collection("rooms").InsertOne(ctx, room); err != nil {
		t.Fatalf("insert room: %v", err)
	}
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	seeds := make([]service.FormalMatchSeed, 2)
	for index := range seeds {
		seed, err := service.BuildFormalMatchSeed(room, now.Add(time.Duration(index)*time.Second))
		if err != nil {
			t.Fatalf("BuildFormalMatchSeed %d: %v", index, err)
		}
		seeds[index] = seed
	}

	type bootstrapResult struct {
		matchID bson.ObjectID
		err     error
	}
	results := make(chan bootstrapResult, len(seeds))
	var wg sync.WaitGroup
	for _, seed := range seeds {
		wg.Add(1)
		go func(seed service.FormalMatchSeed) {
			defer wg.Done()
			bootstrap := persistence.NewFormalMatchBootstrapStore(client, db)
			results <- bootstrapResult{
				matchID: seed.LegacyMatch.ID,
				err:     bootstrap.Create(ctx, room.ID, seed.LegacyMatch, seed.State, now),
			}
		}(seed)
	}
	wg.Wait()
	close(results)

	var winnerID bson.ObjectID
	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winnerID = result.matchID
		case errors.Is(result.err, persistence.ErrFormalMatchAlreadyStarted):
			conflicts++
		default:
			t.Fatalf("concurrent bootstrap error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent bootstrap = %d successes, %d conflicts", successes, conflicts)
	}

	var persistedRoom domain.Room
	if err := db.Collection("rooms").FindOne(ctx, bson.M{"_id": room.ID}).Decode(&persistedRoom); err != nil {
		t.Fatalf("load room: %v", err)
	}
	if persistedRoom.MatchID == nil || *persistedRoom.MatchID != winnerID {
		t.Fatalf("room match id = %v, winner = %s", persistedRoom.MatchID, winnerID)
	}
	if count, err := db.Collection("matches").CountDocuments(ctx, bson.M{"room_id": room.ID}); err != nil || count != 1 {
		t.Fatalf("legacy shells for room = %d, err %v", count, err)
	}
	if count, err := db.Collection(persistence.MatchSnapshotsCollection).CountDocuments(ctx, bson.M{"_id": winnerID}); err != nil || count != 1 {
		t.Fatalf("winner snapshots = %d, err %v", count, err)
	}
	for _, seed := range seeds {
		if seed.LegacyMatch.ID == winnerID {
			continue
		}
		if count, err := db.Collection("matches").CountDocuments(ctx, bson.M{"_id": seed.LegacyMatch.ID}); err != nil || count != 0 {
			t.Fatalf("loser legacy shells = %d, err %v", count, err)
		}
		if count, err := db.Collection(persistence.MatchSnapshotsCollection).CountDocuments(ctx, bson.M{"_id": seed.LegacyMatch.ID}); err != nil || count != 0 {
			t.Fatalf("loser snapshots = %d, err %v", count, err)
		}
	}
}

func TestMongoIntegrationRepositoriesAssignObjectIDs(t *testing.T) {
	_, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	roomRepo := repository.NewRoomRepository(db)
	room := &domain.Room{Code: "IDTEST", Name: "ID Test", Type: domain.RoomTypePrivate, OwnerID: 1, Settings: domain.RoomSettings{Mappool: domain.NewMappool()}}
	if err := roomRepo.Create(ctx, room); err != nil {
		t.Fatalf("create room: %v", err)
	}
	if room.ID == bson.NilObjectID {
		t.Fatal("room repository did not assign an ObjectID")
	}
	room.Name = "Updated ID Test"
	if err := roomRepo.Update(ctx, room); err != nil {
		t.Fatalf("update room after ID assignment: %v", err)
	}
	persistedRoom, err := roomRepo.ByID(ctx, room.ID)
	if err != nil || persistedRoom.Name != room.Name {
		t.Fatalf("updated room = %+v, err %v", persistedRoom, err)
	}

	matchRepo := repository.NewMatchRepository(db)
	match := &domain.Match{RoomID: room.ID, Code: "MATCH-IDTEST", Board: domain.NewBoard(), Mappool: domain.NewMappool()}
	if err := matchRepo.Create(ctx, match); err != nil {
		t.Fatalf("create match: %v", err)
	}
	if match.ID == bson.NilObjectID {
		t.Fatal("match repository did not assign an ObjectID")
	}
	match.Name = "Updated Match ID Test"
	if err := matchRepo.Update(ctx, match); err != nil {
		t.Fatalf("update match after ID assignment: %v", err)
	}
	persistedMatch, err := matchRepo.ByID(ctx, match.ID)
	if err != nil || persistedMatch.Name != match.Name {
		t.Fatalf("updated match = %+v, err %v", persistedMatch, err)
	}
}

type lifecycleFixture struct {
	name    string
	state   matchengine.State
	actor   matchengine.Actor
	command matchengine.Command
	at      time.Time
}

func integrationLifecycleFixtures(t *testing.T, now time.Time) []lifecycleFixture {
	t.Helper()
	ready := integrationReadyState(t)
	running := integrationExecute(t, ready, matchengine.RefereeActor(), matchengine.StartMatch{}, now)
	waitingBase := integrationFirstPick(t, now)
	waiting := integrationExecute(t, waitingBase, matchengine.StrategistActor(waitingBase.ActiveTeam), matchengine.PlacePiece{
		PoolSlotID: "NM5", PieceID: "pending-piece", Cell: "A1",
	}, now.Add(5*time.Second))
	suspended := integrationExecute(t, running, matchengine.RefereeActor(), matchengine.SuspendMatch{Reason: "review"}, now.Add(10*time.Second))
	aborted := integrationExecute(t, running, matchengine.RefereeActor(), matchengine.AbortMatch{Reason: "voided"}, now.Add(10*time.Second))
	finished := integrationExecute(t, running, matchengine.RefereeActor(), matchengine.RecordSurrender{
		SurrenderingTeam: matchengine.TeamRed, ConfirmingPlayerIDs: []int64{1, 2, 3, 4}, Reason: "confirmed",
	}, now.Add(10*time.Second))
	tbBase := integrationFirstPick(t, now)
	tbBase.Turn = 13
	tbRequested := integrationExecute(t, tbBase, matchengine.CaptainActor(tbBase.ActiveTeam), matchengine.RequestTB{
		RequestID: "tb-recovery", Basis: matchengine.TBBasisCaptainAgreement,
	}, now.Add(5*time.Second))
	respondingTeam := matchengine.TeamRed
	if tbBase.ActiveTeam == matchengine.TeamRed {
		respondingTeam = matchengine.TeamBlue
	}
	tbPreparation := integrationExecute(t, tbRequested, matchengine.CaptainActor(respondingTeam), matchengine.RespondTBRequest{
		RequestID: "tb-recovery", Accept: true,
	}, now.Add(6*time.Second))
	tbPlaying := integrationExecute(t, tbPreparation, matchengine.RefereeActor(), matchengine.StartTB{}, now.Add(7*time.Second))
	adjudication := integrationAdjudication(t, now)

	return []lifecycleFixture{
		{name: "ready", state: ready, actor: matchengine.RefereeActor(), command: matchengine.StartMatch{}, at: now},
		{name: "running", state: running, actor: matchengine.StrategistActor(running.ActiveTeam), command: matchengine.BanPoolSlot{PoolSlotID: "NM1"}, at: now.Add(time.Second)},
		{name: "waiting-result", state: waiting, actor: matchengine.RefereeActor(), command: matchengine.ConfirmBeatmapResult{BoardPieceID: "pending-piece", WinningTeam: matchengine.TeamRed}, at: now.Add(6 * time.Second)},
		{name: "suspended", state: suspended, actor: matchengine.RefereeActor(), command: matchengine.ResumeMatch{Reason: "continue"}, at: now.Add(11 * time.Second)},
		{name: "tb-preparation", state: tbPreparation, actor: matchengine.RefereeActor(), command: matchengine.StartTB{}, at: now.Add(7 * time.Second)},
		{name: "tb-playing", state: tbPlaying, actor: matchengine.RefereeActor(), command: matchengine.ConfirmTBResult{WinningTeam: matchengine.TeamBlue}, at: now.Add(8 * time.Second)},
		{name: "finished", state: finished, actor: matchengine.RefereeActor(), command: matchengine.StartMatch{}, at: now.Add(11 * time.Second)},
		{name: "aborted", state: aborted, actor: matchengine.RefereeActor(), command: matchengine.StartMatch{}, at: now.Add(11 * time.Second)},
		{name: "adjudication", state: adjudication, actor: matchengine.RefereeActor(), command: matchengine.StartMatch{}, at: now.Add(11 * time.Second)},
	}
}

func integrationAdjudication(t *testing.T, now time.Time) matchengine.State {
	t.Helper()
	state := integrationFirstPick(t, now)
	state = integrationExecute(t, state, matchengine.StrategistActor(state.ActiveTeam), matchengine.PlacePiece{
		PoolSlotID: "NM5", PieceID: "adjudication-red", Cell: "A1",
	}, now.Add(5*time.Second))
	state = integrationExecute(t, state, matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{
		BoardPieceID: "adjudication-red", WinningTeam: matchengine.TeamRed,
	}, now.Add(6*time.Second))
	state = integrationExecute(t, state, matchengine.StrategistActor(state.ActiveTeam), matchengine.PlacePiece{
		PoolSlotID: "NM6", PieceID: "adjudication-blue", Cell: "B1",
	}, now.Add(7*time.Second))
	state = integrationExecute(t, state, matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{
		BoardPieceID: "adjudication-blue", WinningTeam: matchengine.TeamBlue,
	}, now.Add(8*time.Second))
	state = integrationExecute(t, state, matchengine.StrategistActor(state.ActiveTeam), matchengine.PlaceShiro{
		PieceID: "adjudication-shiro", Cell: "C1",
	}, now.Add(9*time.Second))
	if state.Lifecycle != matchengine.LifecycleAdjudicationRequired {
		t.Fatalf("adjudication fixture lifecycle = %s", state.Lifecycle)
	}
	return state
}

func integrationFirstPick(t *testing.T, now time.Time) matchengine.State {
	t.Helper()
	state := integrationExecute(t, integrationReadyState(t), matchengine.RefereeActor(), matchengine.StartMatch{}, now)
	bans := []string{"NM1", "NM2", "NM3", "NM4"}
	for index, slotID := range bans {
		state = integrationExecute(t, state, matchengine.StrategistActor(state.ActiveTeam), matchengine.BanPoolSlot{PoolSlotID: slotID}, now.Add(time.Duration(index+1)*time.Second))
	}
	return state
}

func integrationReadyState(t *testing.T) matchengine.State {
	t.Helper()
	state, err := matchengine.NewReadyState(matchengine.Configuration{
		FirstBan: matchengine.TeamRed, FirstPick: matchengine.TeamBlue,
		PoolSlots: []matchengine.PoolSlot{
			{ID: "NM1", Mod: matchengine.ModNM}, {ID: "NM2", Mod: matchengine.ModNM},
			{ID: "NM3", Mod: matchengine.ModNM}, {ID: "NM4", Mod: matchengine.ModNM},
			{ID: "NM5", Mod: matchengine.ModNM}, {ID: "NM6", Mod: matchengine.ModNM},
			{ID: "SHIRO", Mod: matchengine.ModShiro}, {ID: "TB", Mod: matchengine.ModTB},
		},
		Rosters: map[matchengine.TeamSide]matchengine.Roster{
			matchengine.TeamRed:  {LeaderID: 1, PlayerIDs: []int64{1, 2, 3, 4, 5, 6, 7, 8}},
			matchengine.TeamBlue: {LeaderID: 11, PlayerIDs: []int64{11, 12, 13, 14, 15, 16, 17, 18}},
		},
		Timers: matchengine.StandardTimerConfiguration(),
	})
	if err != nil {
		t.Fatalf("NewReadyState: %v", err)
	}
	return state
}

func integrationExecute(t *testing.T, state matchengine.State, actor matchengine.Actor, command matchengine.Command, now time.Time) matchengine.State {
	t.Helper()
	transition, err := matchengine.Execute(state, actor, command, now)
	if err != nil {
		t.Fatalf("Execute(%T): %v", command, err)
	}
	return transition.State
}

func integrationMongo(t *testing.T) (*mongo.Client, *mongo.Database) {
	t.Helper()
	uri := os.Getenv(integrationMongoEnv)
	if uri == "" {
		t.Skipf("set %s to run MongoDB replica-set integration tests", integrationMongoEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo.Connect: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		t.Fatalf("mongo.Ping: %v", err)
	}
	var hello struct {
		SetName           string `bson:"setName"`
		IsWritablePrimary bool   `bson:"isWritablePrimary"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		t.Fatalf("mongo hello: %v", err)
	}
	if hello.SetName == "" && os.Getenv(integrationMongoInitReplicaEnv) == "1" {
		replicaHost, err := replicaSetHostFromURI(uri)
		if err != nil {
			t.Fatalf("derive replica-set host from %s: %v", integrationMongoEnv, err)
		}
		initResult := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: bson.M{
			"_id": "rs0", "members": bson.A{bson.M{"_id": 0, "host": replicaHost}},
		}}})
		if err := initResult.Err(); err != nil {
			t.Fatalf("initialize test replica set: %v", err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			hello = struct {
				SetName           string `bson:"setName"`
				IsWritablePrimary bool   `bson:"isWritablePrimary"`
			}{}
			if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err == nil && hello.SetName != "" && hello.IsWritablePrimary {
				break
			}
		}
	}
	if hello.SetName == "" || !hello.IsWritablePrimary {
		t.Fatal("integration MongoDB is not configured as a replica set")
	}
	db := client.Database("rcthub_m3_" + bson.NewObjectID().Hex())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = db.Drop(cleanupCtx)
		_ = client.Disconnect(cleanupCtx)
	})
	return client, db
}

func replicaSetHostFromURI(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "mongodb" {
		return "", fmt.Errorf("automatic replica initialization requires a mongodb URI")
	}
	if parsed.Hostname() == "" || strings.Contains(parsed.Host, ",") {
		return "", fmt.Errorf("automatic replica initialization requires exactly one host")
	}
	port := parsed.Port()
	if port == "" {
		port = "27017"
	}
	return net.JoinHostPort(parsed.Hostname(), port), nil
}

func assertSnapshotIndexes(t *testing.T, ctx context.Context, db *mongo.Database) {
	t.Helper()
	cursor, err := db.Collection(persistence.MatchSnapshotsCollection).Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer cursor.Close(ctx)
	names := make(map[string]bool)
	for cursor.Next(ctx) {
		var index struct {
			Name   string `bson:"name"`
			Unique bool   `bson:"unique"`
		}
		if err := cursor.Decode(&index); err != nil {
			t.Fatalf("decode index: %v", err)
		}
		names[index.Name] = index.Unique
	}
	if err := cursor.Err(); err != nil {
		t.Fatalf("iterate indexes: %v", err)
	}
	if _, ok := names["_id_"]; !ok {
		t.Fatalf("_id index missing: %+v", names)
	}
	for _, name := range []string{"snapshot_schema_updated_at", "snapshot_origin_updated_at"} {
		if _, ok := names[name]; !ok {
			t.Fatalf("index %q missing: %+v", name, names)
		}
	}
}

func assertVersionConflict(t *testing.T, err error, expected, current uint64) {
	t.Helper()
	if !errors.Is(err, persistence.ErrSnapshotVersionConflict) {
		t.Fatalf("CAS error = %v, want version conflict", err)
	}
	var conflict *persistence.SnapshotVersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("CAS error type = %T, want *SnapshotVersionConflictError", err)
	}
	if conflict.Expected != expected || conflict.Current != current {
		t.Fatalf(
			"CAS conflict versions = expected %d/current %d, want expected %d/current %d",
			conflict.Expected,
			conflict.Current,
			expected,
			current,
		)
	}
}

func integrationFormalRoom() domain.Room {
	redStrategist, blueStrategist := int64(101), int64(201)
	redLeader, blueLeader := int64(1), int64(11)
	firstPick, firstBan := domain.TeamSideRed, domain.TeamSideBlue
	mpLink := "https://osu.ppy.sh/community/matches/1"
	pool := domain.NewMappool()
	pool.Slots[domain.PieceModNM] = []domain.Piece{{}, {}, {}, {}, {}}
	pool.Slots[domain.PieceModHD] = []domain.Piece{{}}
	pool.Slots[domain.PieceModHR] = []domain.Piece{{}}
	pool.Slots[domain.PieceModDT] = []domain.Piece{{}}
	pool.Slots[domain.PieceModFM] = []domain.Piece{{}}
	pool.Slots[domain.PieceModShiro] = []domain.Piece{{}}
	pool.Slots[domain.PieceModTB] = []domain.Piece{{}}
	now := time.Date(2026, time.August, 3, 11, 0, 0, 0, time.UTC)
	return domain.Room{
		ID: bson.NewObjectID(), Code: "FORMAL-" + bson.NewObjectID().Hex(), Name: "Formal", Type: domain.RoomTypeMatch, OwnerID: 999,
		Settings: domain.RoomSettings{
			RedStrategistUserID: &redStrategist, BlueStrategistUserID: &blueStrategist,
			Mappool: pool, FirstPick: &firstPick, FirstBan: &firstBan,
			RedPlayers: []int64{1, 2, 3, 4, 5, 6, 7, 8}, BluePlayers: []int64{11, 12, 13, 14, 15, 16, 17, 18},
			RedLeader: &redLeader, BlueLeader: &blueLeader, MPLink: &mpLink,
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func assertJSONEqual(t *testing.T, got, want any) {
	t.Helper()
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	if gotErr != nil || wantErr != nil {
		t.Fatalf("marshal comparison values: got=%v want=%v", gotErr, wantErr)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("JSON differs\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}
