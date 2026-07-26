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

const recentEventLimit = 80

type scenarioName string

const (
	scenarioReady          scenarioName = "ready"
	scenarioFirstPick      scenarioName = "first-pick"
	scenarioRobberyReady   scenarioName = "robbery-ready"
	scenarioTurnThirteen   scenarioName = "turn-13"
	scenarioStalemateFinal scenarioName = "stalemate-final"
)

type lab struct {
	mu       sync.Mutex
	state    matchengine.State
	now      time.Time
	events   []matchengine.Event
	scenario scenarioName
}

type snapshot struct {
	Scenario     scenarioName         `json:"scenario"`
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

func newLab(scenario scenarioName) (*lab, error) {
	state, now, events, err := buildScenario(scenario)
	if err != nil {
		return nil, err
	}
	return &lab{state: state, now: now, events: retainRecentEvents(events), scenario: scenario}, nil
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
		Scenario scenarioName `json:"scenario"`
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
	l.state, l.now, l.events, l.scenario = state, now, retainRecentEvents(events), request.Scenario
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
	l.events = retainRecentEvents(l.events)
	writeJSON(w, http.StatusOK, l.snapshot())
}

func retainRecentEvents(events []matchengine.Event) []matchengine.Event {
	if len(events) > recentEventLimit {
		events = events[len(events)-recentEventLimit:]
	}
	return append([]matchengine.Event(nil), events...)
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

type scenarioPlacement struct {
	poolSlotID   string
	pieceID      string
	cell         matchengine.Cell
	winner       matchengine.TeamSide
	leavePending bool
}

type scenarioCommandApplier func(matchengine.Actor, matchengine.Command) error

func replayScenarioPlacements(state *matchengine.State, apply scenarioCommandApplier, placements []scenarioPlacement) error {
	for _, placement := range placements {
		command := matchengine.PlacePiece{
			PoolSlotID: placement.poolSlotID,
			PieceID:    placement.pieceID,
			Cell:       placement.cell,
		}
		if err := apply(matchengine.StrategistActor(state.ActiveTeam), command); err != nil {
			return fmt.Errorf("place scenario piece %q: %w", placement.pieceID, err)
		}
		if placement.leavePending {
			continue
		}
		result := matchengine.ConfirmBeatmapResult{
			BoardPieceID: placement.pieceID,
			WinningTeam:  placement.winner,
		}
		if err := apply(matchengine.RefereeActor(), result); err != nil {
			return fmt.Errorf("confirm scenario piece %q: %w", placement.pieceID, err)
		}
	}
	return nil
}

func buildScenario(name scenarioName) (matchengine.State, time.Time, []matchengine.Event, error) {
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
	case scenarioReady:
		return state, now, events, nil
	case scenarioFirstPick, scenarioRobberyReady, scenarioTurnThirteen, scenarioStalemateFinal:
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
	if name == scenarioFirstPick {
		return state, now, events, nil
	}
	if name == scenarioStalemateFinal {
		placements := []scenarioPlacement{
			{poolSlotID: "NM5", pieceID: "stalemate-piece-1", cell: "A1", winner: matchengine.TeamRed},
			{poolSlotID: "NM6", pieceID: "stalemate-piece-2", cell: "B1", winner: matchengine.TeamRed},
			{poolSlotID: "NM7", pieceID: "stalemate-piece-3", cell: "C1", winner: matchengine.TeamBlue},
			{poolSlotID: "NM8", pieceID: "stalemate-piece-4", cell: "D1", winner: matchengine.TeamBlue},
			{poolSlotID: "NM9", pieceID: "stalemate-piece-5", cell: "A2", winner: matchengine.TeamBlue},
			{poolSlotID: "NM10", pieceID: "stalemate-piece-6", cell: "B2", winner: matchengine.TeamBlue},
			{poolSlotID: "NM11", pieceID: "stalemate-piece-7", cell: "C2", winner: matchengine.TeamRed},
			{poolSlotID: "NM12", pieceID: "stalemate-piece-8", cell: "D2", winner: matchengine.TeamRed},
			{poolSlotID: "NM13", pieceID: "stalemate-piece-9", cell: "A3", winner: matchengine.TeamRed},
			{poolSlotID: "NM14", pieceID: "stalemate-piece-10", cell: "B3", winner: matchengine.TeamBlue},
			{poolSlotID: "NM15", pieceID: "stalemate-piece-11", cell: "C3", winner: matchengine.TeamRed},
			{poolSlotID: "NM16", pieceID: "stalemate-piece-12", cell: "D3", winner: matchengine.TeamBlue},
			{poolSlotID: "NM17", pieceID: "stalemate-piece-13", cell: "A4", winner: matchengine.TeamBlue},
			{poolSlotID: "NM18", pieceID: "stalemate-piece-14", cell: "B4", winner: matchengine.TeamRed},
			{poolSlotID: "NM19", pieceID: "stalemate-piece-15", cell: "C4", winner: matchengine.TeamBlue},
			{poolSlotID: "NM20", pieceID: "stalemate-piece-16", cell: "D4", leavePending: true},
		}
		if err := replayScenarioPlacements(&state, apply, placements); err != nil {
			return state, now, events, err
		}
		return state, now, events, nil
	}

	placements := []scenarioPlacement{
		{poolSlotID: "NM5", pieceID: "piece-1", cell: "A1", winner: matchengine.TeamBlue},
		{poolSlotID: "NM6", pieceID: "piece-2", cell: "D4", winner: matchengine.TeamRed},
		{poolSlotID: "NM7", pieceID: "piece-3", cell: "B1", winner: matchengine.TeamBlue},
		{poolSlotID: "NM8", pieceID: "piece-4", cell: "D3", winner: matchengine.TeamRed},
		{poolSlotID: "NM9", pieceID: "piece-5", cell: "C1", winner: matchengine.TeamBlue},
		{poolSlotID: "NM10", pieceID: "piece-6", cell: "A3", winner: matchengine.TeamRed},
	}
	if err := replayScenarioPlacements(&state, apply, placements); err != nil {
		return state, now, events, err
	}
	if name == scenarioRobberyReady {
		return state, now, events, nil
	}

	remaining := []scenarioPlacement{
		{poolSlotID: "NM11", pieceID: "piece-7", cell: "A2", winner: matchengine.TeamBlue},
		{poolSlotID: "NM12", pieceID: "piece-8", cell: "B2", winner: matchengine.TeamBlue},
		{poolSlotID: "NM13", pieceID: "piece-9", cell: "C2", winner: matchengine.TeamRed},
		{poolSlotID: "NM14", pieceID: "piece-10", cell: "D2", winner: matchengine.TeamRed},
		{poolSlotID: "NM15", pieceID: "piece-11", cell: "B3", winner: matchengine.TeamBlue},
		{poolSlotID: "NM16", pieceID: "piece-12", cell: "C3", winner: matchengine.TeamRed},
	}
	if err := replayScenarioPlacements(&state, apply, remaining); err != nil {
		return state, now, events, err
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
