package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rctHubBackend/internal/matchengine"
)

func TestBuiltInScenariosAreReplayedThroughValidEngineTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		phase      matchengine.Phase
		turn       int
		activeTeam matchengine.TeamSide
	}{
		{name: "ready", phase: matchengine.PhaseNone, turn: 0},
		{name: "first-pick", phase: matchengine.PhasePick, turn: 1, activeTeam: matchengine.TeamBlue},
		{name: "robbery-ready", phase: matchengine.PhasePick, turn: 7, activeTeam: matchengine.TeamBlue},
		{name: "turn-13", phase: matchengine.PhasePick, turn: 13, activeTeam: matchengine.TeamBlue},
		{name: "stalemate-final", phase: matchengine.PhaseWaitingForResult, turn: 16, activeTeam: matchengine.TeamRed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lab, err := newLab(tt.name)
			if err != nil {
				t.Fatalf("newLab(%q): %v", tt.name, err)
			}
			if lab.state.Phase != tt.phase || lab.state.Turn != tt.turn || lab.state.ActiveTeam != tt.activeTeam {
				t.Fatalf("scenario state = phase %q turn %d active %q", lab.state.Phase, lab.state.Turn, lab.state.ActiveTeam)
			}
			if tt.name == "robbery-ready" && len(lab.state.Board.FindAlignments(matchengine.TeamBlue, 3)) == 0 {
				t.Fatal("robbery-ready scenario lacks the promised three-alignment")
			}
		})
	}
}

func TestStalemateFinalScenarioSupportsAdjudicationAndWonCountResult(t *testing.T) {
	t.Parallel()

	adjudicationLab, err := newLab("stalemate-final")
	if err != nil {
		t.Fatal(err)
	}
	analysis := matchengine.Analyze(adjudicationLab.state)
	if !analysis.Stalemate || analysis.EmptyCells == nil || analysis.LegalPlacements == nil ||
		len(analysis.EmptyCells) != 0 || len(analysis.LegalPlacements) != 0 {
		t.Fatalf("stalemate-final analysis = %+v", analysis)
	}
	response := performJSON(t, adjudicationLab.routes(), http.MethodPost, "/api/command", commandRequest{
		Actor: "REFEREE", Type: "CONFIRM_BEATMAP_RESULT", PieceID: "stalemate-piece-16", WinningTeam: matchengine.TeamRed,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("adjudication status = %d body = %s", response.Code, response.Body.String())
	}
	var adjudication snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &adjudication); err != nil {
		t.Fatal(err)
	}
	if adjudication.State.Lifecycle != matchengine.LifecycleAdjudicationRequired || adjudication.State.Stalemate == nil ||
		adjudication.State.Stalemate.RedWonCount != 8 || adjudication.State.Stalemate.BlueWonCount != 8 {
		t.Fatalf("adjudication state = %+v", adjudication.State)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(`"emptyCells":null`)) || bytes.Contains(response.Body.Bytes(), []byte(`"legalPlacements":null`)) {
		t.Fatalf("adjudication snapshot contains null collections: %s", response.Body.String())
	}

	winnerLab, err := newLab("stalemate-final")
	if err != nil {
		t.Fatal(err)
	}
	response = performJSON(t, winnerLab.routes(), http.MethodPost, "/api/command", commandRequest{
		Actor: "REFEREE", Type: "CONFIRM_BEATMAP_RESULT", PieceID: "stalemate-piece-16", WinningTeam: matchengine.TeamBlue,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("won-count status = %d body = %s", response.Code, response.Body.String())
	}
	var winner snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &winner); err != nil {
		t.Fatal(err)
	}
	if winner.State.Lifecycle != matchengine.LifecycleFinished || winner.State.Winner == nil || *winner.State.Winner != matchengine.TeamBlue ||
		winner.State.Result == nil || winner.State.Result.RedWonCount != 7 || winner.State.Result.BlueWonCount != 9 {
		t.Fatalf("won-count result = %+v", winner.State)
	}
}

func TestCommandEndpointCallsRealEngineAndReturnsSnapshot(t *testing.T) {
	t.Parallel()

	lab, err := newLab("first-pick")
	if err != nil {
		t.Fatal(err)
	}
	request := commandRequest{
		Actor: "BLUE", Type: "PLACE_PIECE", PoolSlotID: "NM5", PieceID: "manual-piece", Cell: "A1",
	}
	response := performJSON(t, lab.routes(), http.MethodPost, "/api/command", request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.State.Phase != matchengine.PhaseWaitingForResult || body.State.PendingPieceID != "manual-piece" {
		t.Fatalf("command response state = %+v", body.State)
	}
	if _, ok := body.State.Board.PieceAt("A1"); !ok {
		t.Fatal("real engine placement missing from board")
	}
}

func TestCommandEndpointPreservesStableRuleErrorCode(t *testing.T) {
	t.Parallel()

	lab, err := newLab("first-pick")
	if err != nil {
		t.Fatal(err)
	}
	request := commandRequest{
		Actor: "BLUE", Type: "PLACE_PIECE", PoolSlotID: "HD1", PieceID: "bad-zone", Cell: "A1",
	}
	response := performJSON(t, lab.routes(), http.MethodPost, "/api/command", request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != string(matchengine.CodeInvalidModZone) {
		t.Fatalf("error code = %q", body["code"])
	}
	if lab.state.Version == 0 || lab.state.Phase != matchengine.PhasePick {
		t.Fatal("failed API command changed scenario state")
	}
}

func TestResetAndVirtualTimeEndpoints(t *testing.T) {
	t.Parallel()

	lab, err := newLab("ready")
	if err != nil {
		t.Fatal(err)
	}
	handler := lab.routes()
	response := performJSON(t, handler, http.MethodPost, "/api/reset", map[string]string{"scenario": "first-pick"})
	if response.Code != http.StatusOK || lab.state.Phase != matchengine.PhasePick {
		t.Fatalf("reset status = %d phase = %q", response.Code, lab.state.Phase)
	}
	before := lab.now
	response = performJSON(t, handler, http.MethodPost, "/api/time", map[string]int{"seconds": 30})
	if response.Code != http.StatusOK || lab.now.Sub(before).Seconds() != 30 {
		t.Fatalf("time status = %d delta = %s", response.Code, lab.now.Sub(before))
	}
}

func TestStaticGUIIsEmbedded(t *testing.T) {
	t.Parallel()

	lab, err := newLab("ready")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	lab.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("MatchEngine Lab")) {
		t.Fatalf("GUI response status = %d", response.Code)
	}
}

func performJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
