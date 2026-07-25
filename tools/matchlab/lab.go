package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"rctHubBackend/internal/matchengine"
)

var labEpoch = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

type lab struct {
	mu       sync.Mutex
	state    matchengine.State
	now      time.Time
	events   []matchengine.Event
	scenario string
}

type snapshot struct {
	Scenario     string               `json:"scenario"`
	Now          time.Time            `json:"now"`
	State        matchengine.State    `json:"state"`
	Analysis     matchengine.Analysis `json:"analysis"`
	RecentEvents []matchengine.Event  `json:"recentEvents"`
	RemainingMS  int64                `json:"remainingMs"`
}

type commandRequest struct {
	Actor               string               `json:"actor"`
	Type                string               `json:"type"`
	ActingTeam          matchengine.TeamSide `json:"actingTeam"`
	PoolSlotID          string               `json:"poolSlotId"`
	PieceID             string               `json:"pieceId"`
	Cell                matchengine.Cell     `json:"cell"`
	WinningTeam         matchengine.TeamSide `json:"winningTeam"`
	TargetPieceID       string               `json:"targetPieceId"`
	SacrificeSets       [][]string           `json:"sacrificeSets"`
	Reason              string               `json:"reason"`
	RequestID           string               `json:"requestId"`
	Basis               matchengine.TBBasis  `json:"basis"`
	Accept              bool                 `json:"accept"`
	RemainingSeconds    int64                `json:"remainingSeconds"`
	SurrenderingTeam    matchengine.TeamSide `json:"surrenderingTeam"`
	ConfirmingPlayerIDs []int64              `json:"confirmingPlayerIds"`
}

func newLab(scenario string) (*lab, error) {
	state, now, events, err := buildScenario(scenario)
	if err != nil {
		return nil, err
	}
	return &lab{state: state, now: now, events: events, scenario: scenario}, nil
}

func (l *lab) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", l.getState)
	mux.HandleFunc("POST /api/reset", l.reset)
	mux.HandleFunc("POST /api/time", l.advanceTime)
	mux.HandleFunc("POST /api/command", l.executeCommand)
	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(static)))
	return mux
}

func (l *lab) getState(w http.ResponseWriter, _ *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	writeJSON(w, http.StatusOK, l.snapshot())
}

func (l *lab) reset(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Scenario string `json:"scenario"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	state, now, events, err := buildScenario(request.Scenario)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_SCENARIO", err.Error())
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state, l.now, l.events, l.scenario = state, now, events, request.Scenario
	writeJSON(w, http.StatusOK, l.snapshot())
}

func (l *lab) advanceTime(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Seconds int64 `json:"seconds"`
	}
	if err := decodeJSON(r, &request); err != nil || request.Seconds < 0 {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "seconds must be a non-negative integer")
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = l.now.Add(time.Duration(request.Seconds) * time.Second)
	writeJSON(w, http.StatusOK, l.snapshot())
}

func (l *lab) executeCommand(w http.ResponseWriter, r *http.Request) {
	var request commandRequest
	if err := decodeJSON(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	actor, err := parseActor(request.Actor)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_ACTOR", err.Error())
		return
	}
	command, err := parseCommand(request)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	transition, err := matchengine.Execute(l.state, actor, command, l.now)
	if err != nil {
		message := err.Error()
		var ruleErr *matchengine.RuleError
		if errors.As(err, &ruleErr) {
			message = ruleErr.Message
		}
		writeAPIError(w, http.StatusConflict, string(matchengine.CodeOf(err)), message)
		return
	}
	l.state = transition.State
	l.events = append(l.events, transition.Events...)
	if len(l.events) > 80 {
		l.events = append([]matchengine.Event(nil), l.events[len(l.events)-80:]...)
	}
	writeJSON(w, http.StatusOK, l.snapshot())
}

func (l *lab) snapshot() snapshot {
	remaining := l.state.Timer.Remaining(l.now)
	return snapshot{
		Scenario: l.scenario, Now: l.now, State: l.state.Clone(),
		Analysis:     matchengine.Analyze(l.state),
		RecentEvents: append([]matchengine.Event(nil), l.events...),
		RemainingMS:  remaining.Milliseconds(),
	}
}

func parseActor(value string) (matchengine.Actor, error) {
	switch strings.ToUpper(value) {
	case "REFEREE":
		return matchengine.RefereeActor(), nil
	case "RED":
		return matchengine.StrategistActor(matchengine.TeamRed), nil
	case "BLUE":
		return matchengine.StrategistActor(matchengine.TeamBlue), nil
	default:
		return matchengine.Actor{}, fmt.Errorf("actor must be REFEREE, RED, or BLUE")
	}
}

func parseCommand(request commandRequest) (matchengine.Command, error) {
	switch strings.ToUpper(request.Type) {
	case "START_MATCH":
		return matchengine.StartMatch{}, nil
	case "BAN_POOL_SLOT":
		return matchengine.BanPoolSlot{PoolSlotID: request.PoolSlotID}, nil
	case "PLACE_PIECE":
		return matchengine.PlacePiece{PoolSlotID: request.PoolSlotID, PieceID: request.PieceID, Cell: request.Cell}, nil
	case "PLACE_SHIRO":
		return matchengine.PlaceShiro{PieceID: request.PieceID, Cell: request.Cell}, nil
	case "ROB_PIECE":
		return matchengine.RobPiece{TargetPieceID: request.TargetPieceID, SacrificeSets: request.SacrificeSets}, nil
	case "CONFIRM_BEATMAP_RESULT":
		return matchengine.ConfirmBeatmapResult{BoardPieceID: request.PieceID, WinningTeam: request.WinningTeam}, nil
	case "GRANT_ADDITIONAL_TIME":
		return matchengine.GrantAdditionalTime{Reason: request.Reason}, nil
	case "CALIBRATE_TIMER":
		return matchengine.CalibrateTimer{Remaining: time.Duration(request.RemainingSeconds) * time.Second, Reason: request.Reason}, nil
	case "PAUSE_TIMER":
		return matchengine.PauseTimer{Reason: request.Reason}, nil
	case "RESUME_TIMER":
		return matchengine.ResumeTimer{Reason: request.Reason}, nil
	case "SUSPEND_MATCH":
		return matchengine.SuspendMatch{Reason: request.Reason}, nil
	case "RESUME_MATCH":
		return matchengine.ResumeMatch{Reason: request.Reason}, nil
	case "SKIP_CURRENT_ACTION":
		return matchengine.SkipCurrentAction{Reason: request.Reason}, nil
	case "ABORT_MATCH":
		return matchengine.AbortMatch{Reason: request.Reason}, nil
	case "REQUEST_TB":
		return matchengine.RequestTB{RequestID: request.RequestID, Basis: request.Basis}, nil
	case "RESPOND_TB_REQUEST":
		return matchengine.RespondTBRequest{RequestID: request.RequestID, Accept: request.Accept}, nil
	case "START_TB":
		return matchengine.StartTB{Reason: request.Reason}, nil
	case "CONFIRM_TB_RESULT":
		return matchengine.ConfirmTBResult{WinningTeam: request.WinningTeam}, nil
	case "RECORD_SURRENDER":
		return matchengine.RecordSurrender{SurrenderingTeam: request.SurrenderingTeam, ConfirmingPlayerIDs: request.ConfirmingPlayerIDs, Reason: request.Reason}, nil
	case "REFEREE_BAN_POOL_SLOT":
		return matchengine.RefereeBanPoolSlot{ActingTeam: request.ActingTeam, PoolSlotID: request.PoolSlotID, Reason: request.Reason}, nil
	case "REFEREE_PLACE_PIECE":
		return matchengine.RefereePlacePiece{ActingTeam: request.ActingTeam, PoolSlotID: request.PoolSlotID, PieceID: request.PieceID, Cell: request.Cell, Reason: request.Reason}, nil
	case "REFEREE_PLACE_SHIRO":
		return matchengine.RefereePlaceShiro{ActingTeam: request.ActingTeam, PieceID: request.PieceID, Cell: request.Cell, Reason: request.Reason}, nil
	case "REFEREE_ROB_PIECE":
		return matchengine.RefereeRobPiece{ActingTeam: request.ActingTeam, TargetPieceID: request.TargetPieceID, SacrificeSets: request.SacrificeSets, Reason: request.Reason}, nil
	case "REFEREE_REQUEST_TB":
		return matchengine.RefereeRequestTB{ActingTeam: request.ActingTeam, RequestID: request.RequestID, Basis: request.Basis, Reason: request.Reason}, nil
	case "REFEREE_RESPOND_TB_REQUEST":
		return matchengine.RefereeRespondTBRequest{ActingTeam: request.ActingTeam, RequestID: request.RequestID, Accept: request.Accept, Reason: request.Reason}, nil
	default:
		return nil, fmt.Errorf("unsupported command type %q", request.Type)
	}
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func buildScenario(name string) (matchengine.State, time.Time, []matchengine.Event, error) {
	state, err := matchengine.NewReadyState(labConfiguration())
	if err != nil {
		return matchengine.State{}, time.Time{}, nil, err
	}
	now := labEpoch
	var events []matchengine.Event
	apply := func(actor matchengine.Actor, command matchengine.Command) error {
		now = now.Add(time.Second)
		transition, executeErr := matchengine.Execute(state, actor, command, now)
		if executeErr != nil {
			return executeErr
		}
		state = transition.State
		events = append(events, transition.Events...)
		return nil
	}

	switch name {
	case "ready":
		return state, now, events, nil
	case "first-pick", "robbery-ready", "turn-13", "stalemate-final":
		if err := apply(matchengine.RefereeActor(), matchengine.StartMatch{}); err != nil {
			return state, now, events, err
		}
		bans := []struct {
			team matchengine.TeamSide
			slot string
		}{{matchengine.TeamRed, "NM1"}, {matchengine.TeamBlue, "NM2"}, {matchengine.TeamBlue, "NM3"}, {matchengine.TeamRed, "NM4"}}
		for _, ban := range bans {
			if err := apply(matchengine.StrategistActor(ban.team), matchengine.BanPoolSlot{PoolSlotID: ban.slot}); err != nil {
				return state, now, events, err
			}
		}
	default:
		return state, now, events, fmt.Errorf("unknown scenario %q", name)
	}
	if name == "first-pick" {
		return state, now, events, nil
	}
	if name == "stalemate-final" {
		placements := []struct {
			cell   matchengine.Cell
			winner matchengine.TeamSide
		}{
			{"A1", matchengine.TeamRed}, {"B1", matchengine.TeamRed}, {"C1", matchengine.TeamBlue}, {"D1", matchengine.TeamBlue},
			{"A2", matchengine.TeamBlue}, {"B2", matchengine.TeamBlue}, {"C2", matchengine.TeamRed}, {"D2", matchengine.TeamRed},
			{"A3", matchengine.TeamRed}, {"B3", matchengine.TeamBlue}, {"C3", matchengine.TeamRed}, {"D3", matchengine.TeamBlue},
			{"A4", matchengine.TeamBlue}, {"B4", matchengine.TeamRed}, {"C4", matchengine.TeamBlue}, {"D4", matchengine.TeamRed},
		}
		for index, placement := range placements {
			slotID := "NM" + strconv.Itoa(index+5)
			pieceID := "stalemate-piece-" + strconv.Itoa(index+1)
			if err := apply(matchengine.StrategistActor(state.ActiveTeam), matchengine.PlacePiece{PoolSlotID: slotID, PieceID: pieceID, Cell: placement.cell}); err != nil {
				return state, now, events, err
			}
			if index == len(placements)-1 {
				return state, now, events, nil
			}
			if err := apply(matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: placement.winner}); err != nil {
				return state, now, events, err
			}
		}
	}

	placements := []struct {
		cell   matchengine.Cell
		winner matchengine.TeamSide
	}{
		{"A1", matchengine.TeamBlue}, {"D4", matchengine.TeamRed},
		{"B1", matchengine.TeamBlue}, {"D3", matchengine.TeamRed},
		{"C1", matchengine.TeamBlue}, {"A3", matchengine.TeamRed},
	}
	for index, placement := range placements {
		slotID := "NM" + strconv.Itoa(index+5)
		pieceID := "piece-" + strconv.Itoa(index+1)
		if err := apply(matchengine.StrategistActor(state.ActiveTeam), matchengine.PlacePiece{PoolSlotID: slotID, PieceID: pieceID, Cell: placement.cell}); err != nil {
			return state, now, events, err
		}
		if err := apply(matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: placement.winner}); err != nil {
			return state, now, events, err
		}
	}
	if name == "robbery-ready" {
		return state, now, events, nil
	}

	remaining := []struct {
		cell   matchengine.Cell
		winner matchengine.TeamSide
	}{
		{"A2", matchengine.TeamBlue}, {"B2", matchengine.TeamBlue},
		{"C2", matchengine.TeamRed}, {"D2", matchengine.TeamRed},
		{"B3", matchengine.TeamBlue}, {"C3", matchengine.TeamRed},
	}
	for index, placement := range remaining {
		slotID := "NM" + strconv.Itoa(index+11)
		pieceID := "piece-" + strconv.Itoa(index+7)
		if err := apply(matchengine.StrategistActor(state.ActiveTeam), matchengine.PlacePiece{PoolSlotID: slotID, PieceID: pieceID, Cell: placement.cell}); err != nil {
			return state, now, events, err
		}
		if err := apply(matchengine.RefereeActor(), matchengine.ConfirmBeatmapResult{BoardPieceID: pieceID, WinningTeam: placement.winner}); err != nil {
			return state, now, events, err
		}
	}
	return state, now, events, nil
}

func labConfiguration() matchengine.Configuration {
	slots := make([]matchengine.PoolSlot, 0, 29)
	for index := 1; index <= 24; index++ {
		slots = append(slots, matchengine.PoolSlot{ID: "NM" + strconv.Itoa(index), Mod: matchengine.ModNM})
	}
	slots = append(slots,
		matchengine.PoolSlot{ID: "HD1", Mod: matchengine.ModHD},
		matchengine.PoolSlot{ID: "HR1", Mod: matchengine.ModHR},
		matchengine.PoolSlot{ID: "DT1", Mod: matchengine.ModDT},
		matchengine.PoolSlot{ID: "FM1", Mod: matchengine.ModFM},
		matchengine.PoolSlot{ID: "SHIRO", Mod: matchengine.ModShiro},
		matchengine.PoolSlot{ID: "TB", Mod: matchengine.ModTB},
	)
	return matchengine.Configuration{
		FirstBan: matchengine.TeamRed, FirstPick: matchengine.TeamBlue,
		PoolSlots: slots, Timers: matchengine.StandardTimerConfiguration(),
		Rosters: map[matchengine.TeamSide]matchengine.Roster{
			matchengine.TeamRed:  {LeaderID: 1001, PlayerIDs: []int64{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008}},
			matchengine.TeamBlue: {LeaderID: 2001, PlayerIDs: []int64{2001, 2002, 2003, 2004, 2005, 2006, 2007, 2008}},
		},
	}
}
