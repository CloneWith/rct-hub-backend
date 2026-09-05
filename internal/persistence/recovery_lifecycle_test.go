package persistence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchengine"
)

func TestSnapshotLifecycleFixturesPreserveAnalysisAndNextExecute(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	for _, fixture := range recoveryLifecycleFixtures(t, now) {
		t.Run(fixture.name, func(t *testing.T) {
			document, err := NewMatchSnapshotDocument(bson.NewObjectID(), fixture.state, now)
			if err != nil {
				t.Fatalf("NewMatchSnapshotDocument: %v", err)
			}
			encoded, err := bson.Marshal(document)
			if err != nil {
				t.Fatalf("bson.Marshal: %v", err)
			}
			var recoveredDocument MatchSnapshotDocument
			if err := bson.Unmarshal(encoded, &recoveredDocument); err != nil {
				t.Fatalf("bson.Unmarshal: %v", err)
			}
			recovered, err := recoveredDocument.DecodeState()
			if err != nil {
				t.Fatalf("DecodeState: %v", err)
			}
			assertRecoveryJSONEqual(t, recovered, fixture.state)
			assertRecoveryJSONEqual(t, matchengine.Analyze(recovered), matchengine.Analyze(fixture.state))

			wantTransition, wantErr := matchengine.Execute(fixture.state, fixture.actor, fixture.command, fixture.at)
			gotTransition, gotErr := matchengine.Execute(recovered, fixture.actor, fixture.command, fixture.at)
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("Execute errors differ: recovered=%v original=%v", gotErr, wantErr)
			}
			assertRecoveryJSONEqual(t, gotTransition, wantTransition)
		})
	}
}

type recoveryFixture struct {
	name    string
	state   matchengine.State
	actor   matchengine.Actor
	command matchengine.Command
	at      time.Time
}

func recoveryLifecycleFixtures(t *testing.T, now time.Time) []recoveryFixture {
	t.Helper()
	ready := recoveryReadyState(t)
	running := recoveryExecute(t, ready, matchengine.RefereeActor(), matchengine.StartMatch{}, now)
	waitingBase := recoveryFirstPick(t, now)
	waiting := recoveryExecute(t, waitingBase, matchengine.StrategistActor(waitingBase.ActiveTeam), matchengine.PlacePiece{
		PoolSlotID: "NM5", PieceID: "pending-piece", Cell: "A1",
	}, now.Add(5*time.Second))
	suspended := recoveryExecute(t, running, matchengine.RefereeActor(), matchengine.SuspendMatch{Reason: "review"}, now.Add(10*time.Second))
	aborted := recoveryExecute(t, running, matchengine.RefereeActor(), matchengine.AbortMatch{Reason: "voided"}, now.Add(10*time.Second))
	finished := recoveryExecute(t, running, matchengine.RefereeActor(), matchengine.RecordSurrender{
		SurrenderingTeam: matchengine.TeamRed, ConfirmingPlayerIDs: []int64{1, 2, 3, 4}, Reason: "confirmed",
	}, now.Add(10*time.Second))
	tbBase := recoveryFirstPick(t, now)
	tbBase.Turn = 13
	tbRequested := recoveryExecute(t, tbBase, matchengine.CaptainActor(tbBase.ActiveTeam), matchengine.RequestTB{
		RequestID: "tb-recovery", Basis: matchengine.TBBasisCaptainAgreement,
	}, now.Add(5*time.Second))
	respondingTeam := matchengine.TeamRed
	if tbBase.ActiveTeam == matchengine.TeamRed {
		respondingTeam = matchengine.TeamBlue
	}
	tbPreparation := recoveryExecute(t, tbRequested, matchengine.CaptainActor(respondingTeam), matchengine.RespondTBRequest{
		RequestID: "tb-recovery", Accept: true,
	}, now.Add(6*time.Second))
	tbPlaying := recoveryExecute(t, tbPreparation, matchengine.RefereeActor(), matchengine.StartTB{}, now.Add(7*time.Second))
	adjudication := recoveryAdjudication(t, now)

	return []recoveryFixture{
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

func recoveryAdjudication(t *testing.T, now time.Time) matchengine.State {
	t.Helper()
	state := recoveryFirstPick(t, now)
	state = recoveryExecute(t, state, matchengine.StrategistActor(state.ActiveTeam), matchengine.PlacePiece{
		PoolSlotID: "NM5", PieceID: "adjudication-red", Cell: "A1",
	}, now.Add(5*time.Second))
	state = recoveryExecute(t, state, matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{
		BoardPieceID: "adjudication-red", WinningTeam: matchengine.TeamRed,
	}, now.Add(6*time.Second))
	state = recoveryExecute(t, state, matchengine.StrategistActor(state.ActiveTeam), matchengine.PlacePiece{
		PoolSlotID: "NM6", PieceID: "adjudication-blue", Cell: "B1",
	}, now.Add(7*time.Second))
	state = recoveryExecute(t, state, matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{
		BoardPieceID: "adjudication-blue", WinningTeam: matchengine.TeamBlue,
	}, now.Add(8*time.Second))
	state = recoveryExecute(t, state, matchengine.StrategistActor(state.ActiveTeam), matchengine.PlaceShiro{
		PieceID: "adjudication-shiro", Cell: "C1",
	}, now.Add(9*time.Second))
	if state.Lifecycle != matchengine.LifecycleAdjudicationRequired {
		t.Fatalf("adjudication fixture lifecycle = %s", state.Lifecycle)
	}
	return state
}

func recoveryFirstPick(t *testing.T, now time.Time) matchengine.State {
	t.Helper()
	state := recoveryExecute(t, recoveryReadyState(t), matchengine.RefereeActor(), matchengine.StartMatch{}, now)
	for index, slotID := range []string{"NM1", "NM2", "NM3", "NM4"} {
		state = recoveryExecute(t, state, matchengine.StrategistActor(state.ActiveTeam), matchengine.BanPoolSlot{PoolSlotID: slotID}, now.Add(time.Duration(index+1)*time.Second))
	}
	return state
}

func recoveryReadyState(t *testing.T) matchengine.State {
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
			matchengine.TeamRed:  {LeaderID: 1, PlayerIDs: []int64{2, 3, 4, 5, 6, 7, 8}},
			matchengine.TeamBlue: {LeaderID: 11, PlayerIDs: []int64{12, 13, 14, 15, 16, 17, 18}},
		},
		Timers: matchengine.StandardTimerConfiguration(),
	})
	if err != nil {
		t.Fatalf("NewReadyState: %v", err)
	}
	return state
}

func recoveryExecute(t *testing.T, state matchengine.State, actor matchengine.Actor, command matchengine.Command, now time.Time) matchengine.State {
	t.Helper()
	transition, err := matchengine.Execute(state, actor, command, now)
	if err != nil {
		t.Fatalf("Execute(%T): %v", command, err)
	}
	return transition.State
}

func assertRecoveryJSONEqual(t *testing.T, got, want any) {
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
