package realtime

import (
	"sort"
	"strconv"
	"time"

	"rctHubBackend/internal/matchengine"
)

// snapshot is the stable browser projection. It intentionally does not expose
// matchengine.State directly, so Go duration units and future engine-only
// fields cannot silently change the realtime contract.
type snapshot struct {
	Version          uint64                `json:"version"`
	Lifecycle        matchengine.Lifecycle `json:"lifecycle"`
	Phase            matchengine.Phase     `json:"phase"`
	FirstBan         matchengine.TeamSide  `json:"firstBan"`
	FirstPick        matchengine.TeamSide  `json:"firstPick"`
	Turn             int                   `json:"turn"`
	ActiveTeam       *matchengine.TeamSide `json:"activeTeam,omitempty"`
	PoolSlots        []poolSlot            `json:"poolSlots"`
	Board            board                 `json:"board"`
	WonCounts        teamCounts            `json:"wonCounts"`
	Timer            timer                 `json:"timer"`
	RobberyUsed      teamFlags             `json:"robberyUsed"`
	TeamPauseUsed    teamFlags             `json:"teamPauseUsed"`
	Rosters          rosters               `json:"rosters"`
	PendingPieceID   string                `json:"pendingPieceId,omitempty"`
	PendingTBRequest *tbRequest            `json:"pendingTBRequest,omitempty"`
	TBEntry          *tbEntry              `json:"tbEntry,omitempty"`
	Winner           *matchengine.TeamSide `json:"winner,omitempty"`
	Result           *matchResult          `json:"result,omitempty"`
	Stalemate        *stalemateEvidence    `json:"stalemate,omitempty"`
}

type poolSlot struct {
	ID    string                    `json:"id"`
	Mod   matchengine.Mod           `json:"mod"`
	State matchengine.PoolSlotState `json:"state"`
}

type board struct {
	Cells []boardCell `json:"cells"`
}

type boardCell struct {
	Cell  string           `json:"cell"`
	Row   int              `json:"row"`
	Col   int              `json:"col"`
	Zone  matchengine.Zone `json:"zone"`
	Piece *boardPiece      `json:"piece,omitempty"`
}

type boardPiece struct {
	ID               string                `json:"id"`
	SourcePoolSlotID string                `json:"sourcePoolSlotId"`
	Mod              matchengine.Mod       `json:"mod"`
	ForceMod         *matchengine.ForceMod `json:"forceMod,omitempty"`
	SelectedBy       matchengine.TeamSide  `json:"selectedBy"`
	Owner            *matchengine.TeamSide `json:"owner,omitempty"`
	Outcome          matchengine.Outcome   `json:"outcome"`
}

type timer struct {
	StartedAt                    *time.Time `json:"startedAt,omitempty"`
	DurationMilliseconds         int64      `json:"durationMilliseconds"`
	Paused                       bool       `json:"paused"`
	RemainingAtPauseMilliseconds *int64     `json:"remainingAtPauseMilliseconds,omitempty"`
}

type teamCounts struct {
	Red  int `json:"red"`
	Blue int `json:"blue"`
}

type teamFlags struct {
	Red  bool `json:"red"`
	Blue bool `json:"blue"`
}

type roster struct {
	LeaderID  string   `json:"leaderId"`
	PlayerIDs []string `json:"playerIds"`
}

type rosters struct {
	Red  roster `json:"red"`
	Blue roster `json:"blue"`
}

type tbRequest struct {
	ID          string               `json:"id"`
	RequestedBy matchengine.TeamSide `json:"requestedBy"`
	Basis       matchengine.TBBasis  `json:"basis"`
}

type tbEntry struct {
	Basis       matchengine.TBBasis  `json:"basis"`
	RequestID   string               `json:"requestId,omitempty"`
	RequestedBy matchengine.TeamSide `json:"requestedBy,omitempty"`
}

type matchResult struct {
	Winner              matchengine.TeamSide     `json:"winner"`
	Reason              matchengine.ResultReason `json:"reason"`
	SurrenderingTeam    *matchengine.TeamSide    `json:"surrenderingTeam,omitempty"`
	ConfirmingPlayerIDs []string                 `json:"confirmingPlayerIds"`
	WonCounts           teamCounts               `json:"wonCounts"`
}

type stalemateEvidence struct {
	WonCounts teamCounts `json:"wonCounts"`
}

func mapSnapshot(state matchengine.State) snapshot {
	analysis := matchengine.Analyze(state)
	result := snapshot{
		Version: state.Version, Lifecycle: state.Lifecycle, Phase: state.Phase,
		FirstBan: state.FirstBan, FirstPick: state.FirstPick, Turn: state.Turn,
		PoolSlots: make([]poolSlot, 0, len(state.PoolSlots)), Board: mapBoard(state.Board),
		WonCounts:      teamCounts{Red: analysis.WonCounts[matchengine.TeamRed], Blue: analysis.WonCounts[matchengine.TeamBlue]},
		Timer:          mapTimer(state.Timer),
		RobberyUsed:    teamFlags{Red: state.RobberyUsed[matchengine.TeamRed], Blue: state.RobberyUsed[matchengine.TeamBlue]},
		TeamPauseUsed:  teamFlags{Red: state.TeamPauseUsed[matchengine.TeamRed], Blue: state.TeamPauseUsed[matchengine.TeamBlue]},
		Rosters:        rosters{Red: mapRoster(state.Rosters[matchengine.TeamRed]), Blue: mapRoster(state.Rosters[matchengine.TeamBlue])},
		PendingPieceID: state.PendingPieceID, PendingTBRequest: mapTBRequest(state.PendingTBRequest),
		TBEntry: mapTBEntry(state.TBEntry), Winner: state.Winner, Result: mapResult(state.Result), Stalemate: mapStalemate(state.Stalemate),
	}
	if state.ActiveTeam == matchengine.TeamRed || state.ActiveTeam == matchengine.TeamBlue {
		active := state.ActiveTeam
		result.ActiveTeam = &active
	}
	for _, slot := range state.PoolSlots {
		result.PoolSlots = append(result.PoolSlots, poolSlot{ID: slot.ID, Mod: slot.Mod, State: slot.State})
	}
	sort.Slice(result.PoolSlots, func(i, j int) bool { return result.PoolSlots[i].ID < result.PoolSlots[j].ID })
	return result
}

func mapBoard(value matchengine.Board) board {
	pieces := value.Pieces()
	cells := make([]boardCell, 0, 16)
	for row := range 4 {
		for col := range 4 {
			id := matchengine.Cell([]byte{byte('A' + col), byte('1' + row)})
			zone, _ := value.ZoneAt(id)
			cell := boardCell{Cell: string(id), Row: row, Col: col, Zone: zone}
			if piece, exists := pieces[id]; exists {
				cell.Piece = mapBoardPiece(piece)
			}
			cells = append(cells, cell)
		}
	}
	return board{Cells: cells}
}

func mapBoardPiece(value matchengine.BoardPiece) *boardPiece {
	return &boardPiece{ID: value.ID, SourcePoolSlotID: value.SourcePoolSlotID, Mod: value.Mod, ForceMod: value.ForceMod, SelectedBy: value.SelectedBy, Owner: value.Owner, Outcome: value.Outcome}
}

func mapTBRequest(value *matchengine.TBRequestState) *tbRequest {
	if value == nil {
		return nil
	}
	return &tbRequest{ID: value.ID, RequestedBy: value.RequestedBy, Basis: value.Basis}
}

func mapTBEntry(value *matchengine.TBEntryState) *tbEntry {
	if value == nil {
		return nil
	}
	return &tbEntry{Basis: value.Basis, RequestID: value.RequestID, RequestedBy: value.RequestedBy}
}

func mapResult(value *matchengine.Result) *matchResult {
	if value == nil {
		return nil
	}
	players := make([]string, len(value.ConfirmingPlayerIDs))
	for index, id := range value.ConfirmingPlayerIDs {
		players[index] = strconv.FormatInt(id, 10)
	}
	return &matchResult{Winner: value.Winner, Reason: value.Reason, SurrenderingTeam: value.SurrenderingTeam, ConfirmingPlayerIDs: players, WonCounts: teamCounts{Red: value.RedWonCount, Blue: value.BlueWonCount}}
}

func mapStalemate(value *matchengine.StalemateEvidence) *stalemateEvidence {
	if value == nil {
		return nil
	}
	return &stalemateEvidence{WonCounts: teamCounts{Red: value.RedWonCount, Blue: value.BlueWonCount}}
}

func mapTimer(value matchengine.Timer) timer {
	result := timer{DurationMilliseconds: value.Duration.Milliseconds(), Paused: value.Paused}
	if !value.StartedAt.IsZero() {
		started := value.StartedAt.UTC()
		result.StartedAt = &started
	}
	if value.Paused {
		remaining := value.RemainingAtPause.Milliseconds()
		result.RemainingAtPauseMilliseconds = &remaining
	}
	return result
}

func mapRoster(value matchengine.Roster) roster {
	players := make([]string, len(value.PlayerIDs))
	for index, id := range value.PlayerIDs {
		players[index] = strconv.FormatInt(id, 10)
	}
	return roster{LeaderID: strconv.FormatInt(value.LeaderID, 10), PlayerIDs: players}
}
