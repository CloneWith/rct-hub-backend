package graphql

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rctHubBackend/internal/matchengine"
)

func TestMatchSnapshotUsesAuthoritativeEngineContract(t *testing.T) {
	state, now := graphqlFormalState(t)
	snapshot := mapMatchSnapshot(state)

	if snapshot.Version != "1" || snapshot.Lifecycle != MatchLifecycleRunning || snapshot.Phase != FormalMatchPhaseBan {
		t.Fatalf("snapshot header = %+v", snapshot)
	}
	if len(snapshot.Board.Cells) != 16 {
		t.Fatalf("board cells = %d, want 16", len(snapshot.Board.Cells))
	}
	wantZones := map[string]FormalBoardZone{"A1": FormalBoardZoneDt, "C1": FormalBoardZoneHd, "A3": FormalBoardZoneHr, "C3": FormalBoardZoneDt}
	for _, cell := range snapshot.Board.Cells {
		if want, exists := wantZones[cell.Cell]; exists && cell.Zone != want {
			t.Errorf("cell %s zone = %s, want %s", cell.Cell, cell.Zone, want)
		}
	}
	if snapshot.Timer.StartedAt == nil || !snapshot.Timer.StartedAt.Equal(now) || snapshot.Timer.DurationMilliseconds != 60000 {
		t.Fatalf("timer = %+v", snapshot.Timer)
	}
}

func TestViewsAreDerivedFromEngineState(t *testing.T) {
	state, now := graphqlFormalState(t)
	strategist := computeStrategistView(state, matchengine.TeamRed, now.Add(time.Second))
	if !strategist.IsMyTurn || strategist.MyTeam != TeamSideRed || len(strategist.Analysis.BanPoolSlotIDs) == 0 {
		t.Fatalf("strategist view = %+v", strategist)
	}
	snapshot := mapMatchSnapshot(state)
	spectator := computeSpectatorView(snapshot)
	if spectator.Lifecycle != MatchLifecycleRunning || spectator.CurrentPhase != FormalMatchPhaseBan || len(spectator.Board.Cells) != 16 {
		t.Fatalf("spectator view = %+v", spectator)
	}
	overlay := computeOverlayView(snapshot)
	if overlay.Timer.StartedAt == nil || overlay.Phase != FormalMatchPhaseBan {
		t.Fatalf("overlay view = %+v", overlay)
	}
	referee := computeRefereeView("507f1f77bcf86cd799439011", MatchStatusReady, state, now.Add(time.Second))
	if referee.Snapshot.Version != "1" || len(referee.Analysis.AllowedActions) == 0 {
		t.Fatalf("referee view = %+v", referee)
	}
}

func TestFormalTimerPreservesMillisecondCalibration(t *testing.T) {
	remaining := 1250 * time.Millisecond
	timer := mapFormalTimer(matchengine.Timer{Duration: 1500 * time.Millisecond, Paused: true, RemainingAtPause: remaining})
	if timer.DurationMilliseconds != 1500 || timer.RemainingAtPauseMilliseconds == nil || *timer.RemainingAtPauseMilliseconds != 1250 {
		t.Fatalf("timer precision = %+v", timer)
	}
}

func TestSchemaExposesTypedFormalMatchContract(t *testing.T) {
	server := NewHandler(NewResolver(nil))
	query := `{ matchType: __type(name: "Match") { fields { name } } resultType: __type(name: "MatchCommandResult") { fields { name } } }`
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+escapeJSON(query)+`}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var body struct {
		Data map[string]struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Errors) != 0 || !hasIntrospectionField(body.Data["matchType"].Fields, "snapshot") || !hasIntrospectionField(body.Data["resultType"].Fields, "snapshot") {
		t.Fatalf("typed contract response = %s", response.Body.String())
	}
	if hasIntrospectionField(body.Data["resultType"].Fields, "state") {
		t.Fatal("raw command state remains public")
	}
}

func graphqlFormalState(t *testing.T) (matchengine.State, time.Time) {
	t.Helper()
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	state, err := matchengine.NewReadyState(matchengine.Configuration{
		FirstBan: matchengine.TeamRed, FirstPick: matchengine.TeamBlue,
		PoolSlots: []matchengine.PoolSlot{
			{ID: "NM1", Mod: matchengine.ModNM}, {ID: "NM2", Mod: matchengine.ModNM},
			{ID: "NM3", Mod: matchengine.ModNM}, {ID: "NM4", Mod: matchengine.ModNM},
			{ID: "HD1", Mod: matchengine.ModHD}, {ID: "HR1", Mod: matchengine.ModHR},
			{ID: "DT1", Mod: matchengine.ModDT}, {ID: "FM1", Mod: matchengine.ModFM},
			{ID: "SHIRO", Mod: matchengine.ModShiro}, {ID: "TB", Mod: matchengine.ModTB},
		},
		Rosters: map[matchengine.TeamSide]matchengine.Roster{
			matchengine.TeamRed:  {LeaderID: 1, PlayerIDs: []int64{2, 3, 4, 5, 6, 7, 8}},
			matchengine.TeamBlue: {LeaderID: 11, PlayerIDs: []int64{12, 13, 14, 15, 16, 17, 18}},
		},
		Timers: matchengine.StandardTimerConfiguration(),
	})
	if err != nil {
		t.Fatal(err)
	}
	transition, err := matchengine.Execute(state, matchengine.RefereeActor(), matchengine.StartMatch{}, now)
	if err != nil {
		t.Fatal(err)
	}
	return transition.State, now
}

func hasIntrospectionField(fields []struct {
	Name string `json:"name"`
}, wanted string) bool {
	for _, field := range fields {
		if field.Name == wanted {
			return true
		}
	}
	return false
}

func escapeJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
