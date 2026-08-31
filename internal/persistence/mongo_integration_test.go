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

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"rctHubBackend/internal/database"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchcommand"
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

func TestMongoIntegrationFormalOrchestratorSurvivesRestart(t *testing.T) {
	client, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	users := repository.NewUserRepository(db)
	rooms := repository.NewRoomRepository(db)
	matches := repository.NewMatchRepository(db)
	teams := repository.NewTeamRepository(db)
	mappools := repository.NewMappoolRepository(db)
	room, redTeam, blueTeam, mappool := integrationFormalRoom()
	refereeID := room.OwnerID
	for _, user := range []*domain.User{
		{ID: bson.NewObjectID(), OnlineID: refereeID, Username: "referee", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}},
		{ID: bson.NewObjectID(), OnlineID: 101, Username: "red-strategist", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleStrategist}},
		{ID: bson.NewObjectID(), OnlineID: 201, Username: "blue-strategist", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleStrategist}},
	} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatalf("create user %d: %v", user.OnlineID, err)
		}
	}
	if err := teams.Create(ctx, &redTeam); err != nil {
		t.Fatalf("create red team: %v", err)
	}
	if err := teams.Create(ctx, &blueTeam); err != nil {
		t.Fatalf("create blue team: %v", err)
	}
	if err := mappools.Create(ctx, &mappool); err != nil {
		t.Fatalf("create mappool: %v", err)
	}
	if err := rooms.Create(ctx, &room); err != nil {
		t.Fatalf("create room: %v", err)
	}

	snapshots := persistence.NewSnapshotStore(db)
	if err := ensureIntegrationCollection(ctx, db, persistence.MatchSnapshotsCollection); err != nil {
		t.Fatal(err)
	}
	if err := snapshots.InstallValidator(ctx); err != nil {
		t.Fatalf("install snapshot validator: %v", err)
	}
	commandStore := persistence.NewCommandStore(client, db)
	if err := commandStore.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure command indexes: %v", err)
	}
	if err := commandStore.InstallValidators(ctx); err != nil {
		t.Fatalf("install command validators: %v", err)
	}

	seedTime := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	seed, err := service.BuildFormalMatchSeed(room, &redTeam, &blueTeam, &mappool, seedTime)
	if err != nil {
		t.Fatalf("BuildFormalMatchSeed: %v", err)
	}
	if err := persistence.NewFormalMatchBootstrapStore(client, db).Create(ctx, room.ID, seed.LegacyMatch, seed.State, seedTime); err != nil {
		t.Fatalf("bootstrap formal match: %v", err)
	}

	newOrchestrator := func() *matchcommand.Orchestrator {
		return matchcommand.NewOrchestrator(
			persistence.NewCommandStore(client, db), users, matches, rooms, teams,
			func() time.Time { return time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC) }, nil,
		)
	}
	orchestrator := newOrchestrator()
	command := func(version uint64, caller int64, value matchengine.Command) matchcommand.Result {
		t.Helper()
		result, executeErr := orchestrator.Execute(ctx, matchcommand.Request{
			MatchID: seed.LegacyMatch.ID, ExpectedVersion: version, CommandID: uuid.NewString(), CallerOsuID: caller, Command: value,
		})
		if executeErr != nil {
			t.Fatalf("execute %T at version %d: %v", value, version, executeErr)
		}
		return result
	}

	started := command(0, refereeID, matchengine.StartMatch{})
	if started.ResultingVersion != 1 || started.State.Phase != matchengine.PhaseBan {
		t.Fatalf("started result = version %d phase %s", started.ResultingVersion, started.State.Phase)
	}
	version := started.ResultingVersion
	state := started.State
	for index, slot := range []string{"NM-1", "NM-2", "NM-3", "NM-4"} {
		caller := int64(101)
		if state.ActiveTeam == matchengine.TeamBlue {
			caller = 201
		}
		result := command(version, caller, matchengine.BanPoolSlot{PoolSlotID: slot})
		version = result.ResultingVersion
		state = result.State
		if result.State.Phase != matchengine.PhasePick && index == 3 {
			t.Fatalf("after final ban phase = %s, want PICK", result.State.Phase)
		}
	}

	// Rebuild all collaborators to model a process restart, then replay the
	// exact StartMatch command from its durable receipt.
	restartedOrchestrator := newOrchestrator()
	replayed, err := restartedOrchestrator.Execute(ctx, matchcommand.Request{
		MatchID: seed.LegacyMatch.ID, ExpectedVersion: 0, CommandID: started.CommandID, CallerOsuID: refereeID, Command: matchengine.StartMatch{},
	})
	if err != nil || replayed.Disposition != matchcommand.DispositionReplayed || replayed.ResultingVersion != 1 {
		t.Fatalf("restart replay = %+v, err=%v", replayed, err)
	}
	placed, err := restartedOrchestrator.Execute(ctx, matchcommand.Request{
		MatchID: seed.LegacyMatch.ID, ExpectedVersion: version, CommandID: uuid.NewString(), CallerOsuID: 101,
		Command: matchengine.PlacePiece{PoolSlotID: "NM-5", PieceID: "restart-piece-1", Cell: "A1"},
	})
	if err != nil || placed.State.Phase != matchengine.PhaseWaitingForResult || placed.State.PendingPieceID != "restart-piece-1" {
		t.Fatalf("restart pick = %+v err=%v", placed, err)
	}
	version = placed.ResultingVersion
	confirmed, err := restartedOrchestrator.Execute(ctx, matchcommand.Request{
		MatchID: seed.LegacyMatch.ID, ExpectedVersion: version, CommandID: uuid.NewString(), CallerOsuID: refereeID,
		Command: matchengine.ConfirmBeatmapResult{BoardPieceID: "restart-piece-1", WinningTeam: matchengine.TeamRed},
	})
	if err != nil || confirmed.State.Phase != matchengine.PhasePick || confirmed.State.PendingPieceID != "" {
		t.Fatalf("restart result confirmation = %+v err=%v", confirmed, err)
	}
	version = confirmed.ResultingVersion
	state = confirmed.State
	for index, turn := range []struct {
		cell matchengine.Cell
		slot string
	}{{"B1", "DT-1"}, {"C1", "HD-1"}, {"D1", "FM-1"}} {
		pieceID := fmt.Sprintf("restart-piece-%d", index+2)
		caller := int64(101)
		if state.ActiveTeam == matchengine.TeamBlue {
			caller = 201
		}
		placed, err = restartedOrchestrator.Execute(ctx, matchcommand.Request{
			MatchID: seed.LegacyMatch.ID, ExpectedVersion: version, CommandID: uuid.NewString(), CallerOsuID: caller,
			Command: matchengine.PlacePiece{PoolSlotID: turn.slot, PieceID: pieceID, Cell: turn.cell},
		})
		if err != nil || placed.State.Phase != matchengine.PhaseWaitingForResult {
			t.Fatalf("follow-up pick %s = %+v err=%v", turn.cell, placed, err)
		}
		version = placed.ResultingVersion
		confirmed, err = restartedOrchestrator.Execute(ctx, matchcommand.Request{
			MatchID: seed.LegacyMatch.ID, ExpectedVersion: version, CommandID: uuid.NewString(), CallerOsuID: refereeID,
			Command: matchengine.ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: matchengine.TeamRed},
		})
		if err != nil {
			t.Fatalf("follow-up result %s = %+v err=%v", turn.cell, confirmed, err)
		}
		version = confirmed.ResultingVersion
		state = confirmed.State
	}
	if state.Lifecycle != matchengine.LifecycleFinished || state.Winner == nil || *state.Winner != matchengine.TeamRed {
		t.Fatalf("terminal state = lifecycle %s winner %v", state.Lifecycle, state.Winner)
	}

	stale, err := restartedOrchestrator.Execute(ctx, matchcommand.Request{
		MatchID: seed.LegacyMatch.ID, ExpectedVersion: 1, CommandID: uuid.NewString(), CallerOsuID: 201, Command: matchengine.BanPoolSlot{PoolSlotID: "NM-5"},
	})
	commandErr := matchcommand.ErrorOf(err)
	if err == nil || stale.ResultingVersion != 0 || commandErr == nil || commandErr.Code != matchcommand.CodeMatchVersionConflict {
		t.Fatalf("stale command result=%+v err=%v", stale, err)
	}

	loaded, err := snapshots.Load(ctx, seed.LegacyMatch.ID)
	persistedPiece, persisted := loaded.Board.PieceAt("A1")
	if err != nil || loaded.Version != version || loaded.Lifecycle != matchengine.LifecycleFinished || !persisted || persistedPiece.ID != "restart-piece-1" {
		t.Fatalf("durable state = version %d lifecycle %s err=%v", loaded.Version, loaded.Lifecycle, err)
	}
	actions, err := commandStore.ListActions(ctx, seed.LegacyMatch.ID, 20)
	if err != nil || len(actions) != 13 {
		t.Fatalf("audit actions = %d err=%v", len(actions), err)
	}
	events, err := commandStore.ListEventsAfter(ctx, seed.LegacyMatch.ID, 0, 100)
	if err != nil || len(events) == 0 || events[0].Sequence != 1 {
		t.Fatalf("durable events = %d first=%+v err=%v", len(events), events, err)
	}
}

func TestMongoIntegrationFormalMatchScenarioReachesNegotiatedTB(t *testing.T) {
	client, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()

	users := repository.NewUserRepository(db)
	rooms := repository.NewRoomRepository(db)
	matches := repository.NewMatchRepository(db)
	teams := repository.NewTeamRepository(db)
	mappools := repository.NewMappoolRepository(db)
	room, redTeam, blueTeam, mappool := integrationFormalRoom()
	refereeID := room.OwnerID
	for _, user := range []*domain.User{
		{ID: bson.NewObjectID(), OnlineID: refereeID, Username: "referee", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}},
		{ID: bson.NewObjectID(), OnlineID: 101, Username: "red-strategist", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleStrategist}},
		{ID: bson.NewObjectID(), OnlineID: 201, Username: "blue-strategist", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleStrategist}},
		{ID: bson.NewObjectID(), OnlineID: 1, Username: "red-captain", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RolePlayer}},
		{ID: bson.NewObjectID(), OnlineID: 11, Username: "blue-captain", VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RolePlayer}},
	} {
		if err := users.Create(ctx, user); err != nil {
			t.Fatalf("create user %d: %v", user.OnlineID, err)
		}
	}
	if err := teams.Create(ctx, &redTeam); err != nil {
		t.Fatalf("create red team: %v", err)
	}
	if err := teams.Create(ctx, &blueTeam); err != nil {
		t.Fatalf("create blue team: %v", err)
	}
	if err := mappools.Create(ctx, &mappool); err != nil {
		t.Fatalf("create mappool: %v", err)
	}
	if err := rooms.Create(ctx, &room); err != nil {
		t.Fatalf("create room: %v", err)
	}
	snapshots := persistence.NewSnapshotStore(db)
	if err := ensureIntegrationCollection(ctx, db, persistence.MatchSnapshotsCollection); err != nil {
		t.Fatal(err)
	}
	if err := snapshots.InstallValidator(ctx); err != nil {
		t.Fatalf("install snapshot validator: %v", err)
	}
	commandStore := persistence.NewCommandStore(client, db)
	if err := commandStore.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure command indexes: %v", err)
	}
	if err := commandStore.InstallValidators(ctx); err != nil {
		t.Fatalf("install command validators: %v", err)
	}

	seedTime := time.Date(2026, time.August, 14, 13, 0, 0, 0, time.UTC)
	seed, err := service.BuildFormalMatchSeed(room, &redTeam, &blueTeam, &mappool, seedTime)
	if err != nil {
		t.Fatalf("BuildFormalMatchSeed: %v", err)
	}
	if err := persistence.NewFormalMatchBootstrapStore(client, db).Create(ctx, room.ID, seed.LegacyMatch, seed.State, seedTime); err != nil {
		t.Fatalf("bootstrap formal match: %v", err)
	}
	newOrchestrator := func() *matchcommand.Orchestrator {
		return matchcommand.NewOrchestrator(
			persistence.NewCommandStore(client, db), users, matches, rooms, teams,
			func() time.Time { return seedTime.Add(time.Minute) }, nil,
		)
	}
	orchestrator := newOrchestrator()
	version := uint64(0)
	apply := func(caller int64, command matchengine.Command) matchcommand.Result {
		t.Helper()
		result, executeErr := orchestrator.Execute(ctx, matchcommand.Request{
			MatchID: seed.LegacyMatch.ID, ExpectedVersion: version, CommandID: uuid.NewString(), CallerOsuID: caller, Command: command,
		})
		if executeErr != nil {
			t.Fatalf("execute %T at version %d: %v", command, version, executeErr)
		}
		version = result.ResultingVersion
		return result
	}

	state := apply(refereeID, matchengine.StartMatch{}).State
	for _, slot := range []string{"NM-1", "NM-2", "NM-3", "NM-4"} {
		caller := int64(101)
		if state.ActiveTeam == matchengine.TeamBlue {
			caller = 201
		}
		state = apply(caller, matchengine.BanPoolSlot{PoolSlotID: slot}).State
	}
	opening := []struct {
		cell   matchengine.Cell
		winner matchengine.TeamSide
	}{{"A1", matchengine.TeamRed}, {"D4", matchengine.TeamBlue}, {"B1", matchengine.TeamRed}, {"D2", matchengine.TeamRed}, {"C1", matchengine.TeamRed}, {"D3", matchengine.TeamRed}}
	state = snapshotsState(t, ctx, snapshots, seed.LegacyMatch.ID)
	for index, placement := range opening {
		caller := int64(101)
		if state.ActiveTeam == matchengine.TeamBlue {
			caller = 201
		}
		pieceID := fmt.Sprintf("scenario-piece-%d", index+1)
		apply(caller, matchengine.PlacePiece{PoolSlotID: fmt.Sprintf("NM-%d", index+5), PieceID: pieceID, Cell: placement.cell})
		apply(refereeID, matchengine.ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: placement.winner})
		state = snapshotsState(t, ctx, snapshots, seed.LegacyMatch.ID)
	}
	apply(refereeID, matchengine.PauseTimer{Reason: "network verification"})
	apply(refereeID, matchengine.ResumeTimer{Reason: "network stable"})
	caller := int64(101)
	if state.ActiveTeam == matchengine.TeamBlue {
		caller = 201
	}
	apply(caller, matchengine.RobPiece{TargetPieceID: "scenario-piece-2", SacrificeSets: [][]string{{"scenario-piece-1", "scenario-piece-3", "scenario-piece-5"}}})
	closing := []struct {
		cell   matchengine.Cell
		winner matchengine.TeamSide
	}{{"A2", matchengine.TeamRed}, {"B2", matchengine.TeamBlue}, {"C2", matchengine.TeamRed}, {"A3", matchengine.TeamRed}, {"B3", matchengine.TeamBlue}, {"C3", matchengine.TeamRed}}
	state = snapshotsState(t, ctx, snapshots, seed.LegacyMatch.ID)
	for index, placement := range closing {
		caller := int64(101)
		if state.ActiveTeam == matchengine.TeamBlue {
			caller = 201
		}
		pieceID := fmt.Sprintf("scenario-piece-%d", index+7)
		apply(caller, matchengine.PlacePiece{PoolSlotID: fmt.Sprintf("NM-%d", index+11), PieceID: pieceID, Cell: placement.cell})
		apply(refereeID, matchengine.ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: placement.winner})
		state = snapshotsState(t, ctx, snapshots, seed.LegacyMatch.ID)
	}

	// Recreate the application service before terminal operations to verify that
	// the same Mongo aggregate is sufficient after a process restart.
	orchestrator = newOrchestrator()
	apply(1, matchengine.RequestTB{RequestID: "mongo-scenario-tb", Basis: matchengine.TBBasisCaptainAgreement})
	apply(11, matchengine.RespondTBRequest{RequestID: "mongo-scenario-tb", Accept: true})
	apply(refereeID, matchengine.StartTB{Reason: "lobby ready"})
	final := apply(refereeID, matchengine.ConfirmTBResult{WinningTeam: matchengine.TeamRed})
	if final.State.Lifecycle != matchengine.LifecycleFinished || final.State.Winner == nil || *final.State.Winner != matchengine.TeamRed {
		t.Fatalf("scenario terminal state = %+v", final.State)
	}
	if version != 36 {
		t.Fatalf("scenario version = %d, want 36", version)
	}
	actions, err := commandStore.ListActions(ctx, seed.LegacyMatch.ID, 100)
	if err != nil || len(actions) != 36 {
		t.Fatalf("scenario audit actions = %d err=%v", len(actions), err)
	}
	events, err := commandStore.ListEventsAfter(ctx, seed.LegacyMatch.ID, 0, 200)
	if err != nil || len(events) == 0 || events[len(events)-1].Sequence == 0 {
		t.Fatalf("scenario durable events = %d err=%v", len(events), err)
	}
}

func snapshotsState(t *testing.T, ctx context.Context, snapshots *persistence.SnapshotStore, matchID bson.ObjectID) matchengine.State {
	t.Helper()
	state, err := snapshots.Load(ctx, matchID)
	if err != nil {
		t.Fatalf("load scenario state: %v", err)
	}
	return state
}

func ensureIntegrationCollection(ctx context.Context, db *mongo.Database, name string) error {
	if err := db.CreateCollection(ctx, name); err != nil {
		var commandErr mongo.CommandError
		if errors.As(err, &commandErr) && commandErr.Code == 48 {
			return nil
		}
		return fmt.Errorf("create %s: %w", name, err)
	}
	return nil
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
		wg.Go(func() {
			results <- store.CompareAndSwap(ctx, matchID, started.Version, next, now.Add(2*time.Second))
		})
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
	room, redTeam, blueTeam, mappool := integrationFormalRoom()
	if _, err := db.Collection("rooms").InsertOne(ctx, room); err != nil {
		t.Fatalf("insert room: %v", err)
	}
	seed, err := service.BuildFormalMatchSeed(room, &redTeam, &blueTeam, &mappool, now)
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

	rollbackRoom, rollbackRed, rollbackBlue, rollbackPool := integrationFormalRoom()
	rollbackRoom.Code = "ROLLBACK"
	if _, err := db.Collection("rooms").InsertOne(ctx, rollbackRoom); err != nil {
		t.Fatalf("insert rollback room: %v", err)
	}
	rollbackSeed, err := service.BuildFormalMatchSeed(rollbackRoom, &rollbackRed, &rollbackBlue, &rollbackPool, now)
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

	room, redTeam, blueTeam, mappool := integrationFormalRoom()
	if _, err := db.Collection("rooms").InsertOne(ctx, room); err != nil {
		t.Fatalf("insert room: %v", err)
	}
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	seeds := make([]service.FormalMatchSeed, 2)
	for index := range seeds {
		seed, err := service.BuildFormalMatchSeed(room, &redTeam, &blueTeam, &mappool, now.Add(time.Duration(index)*time.Second))
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
	room := &domain.Room{Code: "IDTEST", Name: "ID Test", Type: domain.RoomTypePrivate, OwnerID: 1}
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
	match := &domain.Match{RoomID: room.ID, Code: "MATCH-IDTEST", Board: domain.NewBoard(), Mappool: domain.NewPool()}
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

// integrationFormalRoom returns a fully configured tournament room plus the
// linked team and mappool entities it references. The mappool carries 20 NM
// entries plus one entry for every other mod so ban/pick scenarios always
// have spare NM slots.
func integrationFormalRoom() (domain.Room, domain.Team, domain.Team, domain.Mappool) {
	redStrategist, blueStrategist := int64(101), int64(201)
	redLeader, blueLeader := int64(1), int64(11)
	refereeID := int64(999)
	firstPick, firstBan := domain.TeamSideRed, domain.TeamSideBlue
	mpLink := "https://osu.ppy.sh/community/matches/1"

	redTeam := domain.Team{
		ID:           bson.NewObjectID(),
		Name:         "Integration Red",
		LeaderID:     &redLeader,
		StrategistID: &redStrategist,
		Players:      []int64{1, 2, 3, 4, 5, 6, 7, 8},
	}
	blueTeam := domain.Team{
		ID:           bson.NewObjectID(),
		Name:         "Integration Blue",
		LeaderID:     &blueLeader,
		StrategistID: &blueStrategist,
		Players:      []int64{11, 12, 13, 14, 15, 16, 17, 18},
	}

	beatmap := int64(1000000)
	entries := make([]domain.MappoolEntry, 0, 26)
	for index := 1; index <= 20; index++ {
		entries = append(entries, domain.MappoolEntry{Mod: domain.PieceModNM, Index: index, BeatmapID: &beatmap})
	}
	for _, mod := range []domain.PieceMod{domain.PieceModHD, domain.PieceModHR, domain.PieceModDT, domain.PieceModFM, domain.PieceModTB} {
		entries = append(entries, domain.MappoolEntry{Mod: mod, Index: 1, BeatmapID: &beatmap})
	}
	entries = append(entries, domain.MappoolEntry{Mod: domain.PieceModShiro, Index: 1})
	mappool := domain.Mappool{ID: bson.NewObjectID(), Name: "Integration Pool", Entries: entries}

	now := time.Date(2026, time.August, 3, 11, 0, 0, 0, time.UTC)
	room := domain.Room{
		ID: bson.NewObjectID(), Code: "FORMAL-" + bson.NewObjectID().Hex(), Name: "Formal", Type: domain.RoomTypeMatch, OwnerID: 999, RefereeUserID: &refereeID, ScheduledAt: &now,
		Settings: domain.RoomSettings{
			RedTeamID:  &redTeam.ID,
			BlueTeamID: &blueTeam.ID,
			MappoolID:  &mappool.ID,
			FirstPick:  &firstPick,
			FirstBan:   &firstBan,
			MPLink:     &mpLink,
		},
		CreatedAt: now, UpdatedAt: now,
	}
	return room, redTeam, blueTeam, mappool
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
