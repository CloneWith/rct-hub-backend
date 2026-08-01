package persistence

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchengine"
)

func TestMatchSnapshotBSONRoundTripPreservesEngineBehavior(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	state := snapshotTestState(t, now)
	matchID := bson.NewObjectID()
	document, err := NewMatchSnapshotDocument(matchID, state, now)
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	encoded, err := bson.Marshal(document)
	if err != nil {
		t.Fatalf("marshal snapshot BSON: %v", err)
	}
	if _, ok := bson.Raw(encoded).Lookup("state").DocumentOK(); !ok {
		t.Fatalf("snapshot state is not an embedded BSON document: %v", bson.Raw(encoded).Lookup("state").Type)
	}
	var recoveredDocument MatchSnapshotDocument
	if err := bson.Unmarshal(encoded, &recoveredDocument); err != nil {
		t.Fatalf("unmarshal snapshot BSON: %v", err)
	}
	recovered, err := recoveredDocument.DecodeState()
	if err != nil {
		t.Fatalf("decode recovered state: %v", err)
	}

	wantJSON, _ := json.Marshal(state)
	gotJSON, _ := json.Marshal(recovered)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("state changed across BSON round trip\n got: %s\nwant: %s", gotJSON, wantJSON)
	}

	actor := matchengine.StrategistActor(state.ActiveTeam)
	command := matchengine.PlaceShiro{PieceID: "shiro-piece", Cell: "B1"}
	wantTransition, wantErr := matchengine.Execute(state, actor, command, now.Add(10*time.Second))
	gotTransition, gotErr := matchengine.Execute(recovered, actor, command, now.Add(10*time.Second))
	if wantErr != nil || gotErr != nil {
		t.Fatalf("execute after recovery: original=%v recovered=%v", wantErr, gotErr)
	}
	wantTransitionJSON, _ := json.Marshal(wantTransition)
	gotTransitionJSON, _ := json.Marshal(gotTransition)
	if !bytes.Equal(gotTransitionJSON, wantTransitionJSON) {
		t.Fatalf("behavior changed after recovery\n got: %s\nwant: %s", gotTransitionJSON, wantTransitionJSON)
	}
}

func TestMatchSnapshotRejectsUnsupportedOrInconsistentDocuments(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	state := snapshotTestState(t, now)
	document, err := NewMatchSnapshotDocument(bson.NewObjectID(), state, now)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*MatchSnapshotDocument)
	}{
		{name: "unsupported schema", mutate: func(d *MatchSnapshotDocument) { d.SchemaVersion++ }},
		{name: "version mismatch", mutate: func(d *MatchSnapshotDocument) { d.MatchVersion++ }},
		{name: "missing state", mutate: func(d *MatchSnapshotDocument) { d.State = nil }},
		{name: "invalid BSON state", mutate: func(d *MatchSnapshotDocument) { d.State = bson.Raw{1, 2, 3} }},
		{name: "missing match ID", mutate: func(d *MatchSnapshotDocument) { d.MatchID = bson.NilObjectID }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := document
			candidate.State = append(bson.Raw(nil), document.State...)
			tt.mutate(&candidate)
			if _, err := candidate.DecodeState(); err == nil {
				t.Fatal("DecodeState accepted invalid snapshot")
			}
		})
	}
	if _, err := NewMatchSnapshotDocument(bson.NilObjectID, state, now); err == nil {
		t.Fatal("NewMatchSnapshotDocument accepted a missing match ID")
	}
}

func snapshotTestState(t *testing.T, now time.Time) matchengine.State {
	t.Helper()

	configuration := matchengine.Configuration{
		FirstBan:  matchengine.TeamRed,
		FirstPick: matchengine.TeamBlue,
		PoolSlots: []matchengine.PoolSlot{
			{ID: "NM1", Mod: matchengine.ModNM},
			{ID: "NM2", Mod: matchengine.ModNM},
			{ID: "NM3", Mod: matchengine.ModNM},
			{ID: "NM4", Mod: matchengine.ModNM},
			{ID: "NM5", Mod: matchengine.ModNM},
			{ID: "SHIRO", Mod: matchengine.ModShiro},
			{ID: "TB", Mod: matchengine.ModTB},
		},
		Rosters: map[matchengine.TeamSide]matchengine.Roster{
			matchengine.TeamRed:  {LeaderID: 1, PlayerIDs: []int64{1, 2, 3, 4, 5, 6, 7, 8}},
			matchengine.TeamBlue: {LeaderID: 11, PlayerIDs: []int64{11, 12, 13, 14, 15, 16, 17, 18}},
		},
		Timers: matchengine.StandardTimerConfiguration(),
	}
	state, err := matchengine.NewReadyState(configuration)
	if err != nil {
		t.Fatalf("new ready state: %v", err)
	}
	apply := func(actor matchengine.Actor, command matchengine.Command) {
		t.Helper()
		now = now.Add(time.Second)
		transition, executeErr := matchengine.Execute(state, actor, command, now)
		if executeErr != nil {
			t.Fatalf("execute %T: %v", command, executeErr)
		}
		state = transition.State
	}
	apply(matchengine.RefereeActor(), matchengine.StartMatch{})
	apply(matchengine.StrategistActor(matchengine.TeamRed), matchengine.BanPoolSlot{PoolSlotID: "NM1"})
	apply(matchengine.StrategistActor(matchengine.TeamBlue), matchengine.BanPoolSlot{PoolSlotID: "NM2"})
	apply(matchengine.StrategistActor(matchengine.TeamBlue), matchengine.BanPoolSlot{PoolSlotID: "NM3"})
	apply(matchengine.StrategistActor(matchengine.TeamRed), matchengine.BanPoolSlot{PoolSlotID: "NM4"})
	apply(matchengine.StrategistActor(matchengine.TeamBlue), matchengine.PlacePiece{PoolSlotID: "NM5", PieceID: "piece-1", Cell: "A1"})
	apply(matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{BoardPieceID: "piece-1", WinningTeam: matchengine.TeamRed})
	return state
}
