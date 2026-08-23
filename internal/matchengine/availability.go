package matchengine

import (
	"sort"
	"time"
)

// PlacementOption is a server-derived legal PoolSlot/cell pairing.
type PlacementOption struct {
	PoolSlotID string    `json:"poolSlotId"`
	Cell       Cell      `json:"cell"`
	ForceMod   *ForceMod `json:"forceMod,omitempty"`
}

// Analysis is a deterministic, read-only view of currently possible play.
// It is shared by terminal evaluation and non-authoritative tooling.
type Analysis struct {
	SelectablePoolSlotIDs []string          `json:"selectablePoolSlotIds"`
	EmptyCells            []Cell            `json:"emptyCells"`
	LegalCellsByPoolSlot  map[string][]Cell `json:"legalCellsByPoolSlot"`
	LegalPlacements       []PlacementOption `json:"legalPlacements"`
	WonCounts             map[TeamSide]int  `json:"wonCounts"`
	Stalemate             bool              `json:"stalemate"`
}

// Action is a stable client-facing command capability. It names an intent,
// not a transport mutation, so HTTP and future realtime clients can share it.
type Action string

const (
	ActionStartMatch          Action = "START_MATCH"
	ActionBanPoolSlot         Action = "BAN_POOL_SLOT"
	ActionPlacePiece          Action = "PLACE_PIECE"
	ActionPlaceShiro          Action = "PLACE_SHIRO"
	ActionRobPiece            Action = "ROB_PIECE"
	ActionConfirmResult       Action = "CONFIRM_BEATMAP_RESULT"
	ActionGrantAdditionalTime Action = "GRANT_ADDITIONAL_TIME"
	ActionCalibrateTimer      Action = "CALIBRATE_TIMER"
	ActionPauseTimer          Action = "PAUSE_TIMER"
	ActionResumeTimer         Action = "RESUME_TIMER"
	ActionSuspendMatch        Action = "SUSPEND_MATCH"
	ActionResumeMatch         Action = "RESUME_MATCH"
	ActionSkipCurrentAction   Action = "SKIP_CURRENT_ACTION"
	ActionAbortMatch          Action = "ABORT_MATCH"
	ActionRequestTB           Action = "REQUEST_TB"
	ActionRespondTBRequest    Action = "RESPOND_TB_REQUEST"
	ActionStartTB             Action = "START_TB"
	ActionConfirmTBResult     Action = "CONFIRM_TB_RESULT"
	ActionRecordSurrender     Action = "RECORD_SURRENDER"
)

// RobberyPlan is one complete engine-valid target and sacrifice choice.
// SacrificeSets preserves alignment boundaries required by RobPiece.
type RobberyPlan struct {
	TargetPieceID string     `json:"targetPieceId"`
	SacrificeSets [][]string `json:"sacrificeSets"`
}

// ActorAnalysis is the deterministic, read-only command surface for one
// already-authorized actor at an explicit server time.
type ActorAnalysis struct {
	AllowedActions     []Action          `json:"allowedActions"`
	BanPoolSlotIDs     []string          `json:"banPoolSlotIds"`
	LegalPlacements    []PlacementOption `json:"legalPlacements"`
	ShiroCells         []Cell            `json:"shiroCells"`
	RobberyPlans       []RobberyPlan     `json:"robberyPlans"`
	PendingTBRequestID string            `json:"pendingTbRequestId,omitempty"`
	CanAcceptTBRequest bool              `json:"canAcceptTbRequest"`
	CanRejectTBRequest bool              `json:"canRejectTbRequest"`
	TBRequestTeams     []TeamSide        `json:"tbRequestTeams"`
	TBResponseTeams    []TeamSide        `json:"tbResponseTeams"`
}

// Analyze computes availability from state without mutating it.
func Analyze(state State) Analysis {
	analysis := Analysis{
		SelectablePoolSlotIDs: []string{},
		EmptyCells:            []Cell{},
		LegalCellsByPoolSlot:  make(map[string][]Cell),
		LegalPlacements:       []PlacementOption{},
		WonCounts:             map[TeamSide]int{TeamRed: 0, TeamBlue: 0},
	}
	for row := range 4 {
		for column := range 4 {
			cell := positionCell(column, row)
			if state.Board.empty(cell) {
				analysis.EmptyCells = append(analysis.EmptyCells, cell)
			}
		}
	}

	for _, piece := range state.Board.pieces {
		if piece.Outcome == OutcomeWon && piece.Owner != nil && piece.Owner.valid() {
			analysis.WonCounts[*piece.Owner]++
		}
	}

	for id, slot := range state.PoolSlots {
		if slot.State != PoolSlotAvailable || slot.Mod == ModTB {
			continue
		}
		analysis.SelectablePoolSlotIDs = append(analysis.SelectablePoolSlotIDs, id)
	}
	sort.Strings(analysis.SelectablePoolSlotIDs)

	for _, slotID := range analysis.SelectablePoolSlotIDs {
		slot := state.PoolSlots[slotID]
		for _, cell := range analysis.EmptyCells {
			if slot.Mod == ModShiro {
				analysis.LegalCellsByPoolSlot[slotID] = append(analysis.LegalCellsByPoolSlot[slotID], cell)
				analysis.LegalPlacements = append(analysis.LegalPlacements, PlacementOption{PoolSlotID: slotID, Cell: cell})
				continue
			}
			zone, _ := state.Board.ZoneAt(cell)
			forceMod, err := placementForceMod(slot.Mod, zone)
			if err != nil {
				continue
			}
			analysis.LegalCellsByPoolSlot[slotID] = append(analysis.LegalCellsByPoolSlot[slotID], cell)
			analysis.LegalPlacements = append(analysis.LegalPlacements, PlacementOption{
				PoolSlotID: slotID, Cell: cell, ForceMod: forceMod,
			})
		}
		if _, exists := analysis.LegalCellsByPoolSlot[slotID]; !exists {
			analysis.LegalCellsByPoolSlot[slotID] = []Cell{}
		}
	}

	analysis.Stalemate = len(analysis.SelectablePoolSlotIDs) == 0 || len(analysis.LegalPlacements) == 0
	return analysis
}

// AnalyzeForActor derives the exact selectable command surface without
// mutating state. Authorization of the user-to-actor mapping remains an outer
// layer concern; this function owns only match rules.
func AnalyzeForActor(state State, actor Actor, now time.Time) ActorAnalysis {
	result := ActorAnalysis{
		AllowedActions:  []Action{},
		BanPoolSlotIDs:  []string{},
		LegalPlacements: []PlacementOption{},
		ShiroCells:      []Cell{},
		RobberyPlans:    []RobberyPlan{},
		TBRequestTeams:  []TeamSide{},
		TBResponseTeams: []TeamSide{},
	}
	if now.IsZero() {
		return result
	}

	if actor.Capability == CapabilityReferee {
		return analyzeReferee(state, now)
	}
	if actor.Team == nil || !actor.Team.valid() {
		return result
	}

	switch actor.Capability {
	case CapabilityStrategist:
		if state.Lifecycle != LifecycleRunning || state.ActiveTeam != *actor.Team || state.Timer.Paused || state.Timer.expired(now) {
			return result
		}
		switch state.Phase {
		case PhaseBan:
			for id, slot := range state.PoolSlots {
				if slot.State == PoolSlotAvailable && slot.Mod != ModShiro && slot.Mod != ModTB {
					result.BanPoolSlotIDs = append(result.BanPoolSlotIDs, id)
				}
			}
			sort.Strings(result.BanPoolSlotIDs)
			if len(result.BanPoolSlotIDs) > 0 {
				result.AllowedActions = append(result.AllowedActions, ActionBanPoolSlot)
			}
		case PhasePick:
			base := Analyze(state)
			for _, placement := range base.LegalPlacements {
				if state.PoolSlots[placement.PoolSlotID].Mod != ModShiro {
					result.LegalPlacements = append(result.LegalPlacements, placement)
				}
			}
			if len(result.LegalPlacements) > 0 {
				result.AllowedActions = append(result.AllowedActions, ActionPlacePiece)
			}
			if shiroAvailable(state) && len(base.EmptyCells) > 0 {
				result.AllowedActions = append(result.AllowedActions, ActionPlaceShiro)
				result.ShiroCells = append(result.ShiroCells, base.EmptyCells...)
			}
			result.RobberyPlans = robberyPlans(state, *actor.Team)
			if len(result.RobberyPlans) > 0 {
				result.AllowedActions = append(result.AllowedActions, ActionRobPiece)
			}
		}
	case CapabilityCaptain:
		if state.Lifecycle != LifecycleRunning || state.Phase != PhasePick || state.Timer.Paused || state.Turn < 11 || state.Turn > 14 {
			return result
		}
		if state.PendingTBRequest == nil {
			result.AllowedActions = append(result.AllowedActions, ActionRequestTB)
		} else if state.PendingTBRequest.RequestedBy != *actor.Team {
			result.PendingTBRequestID = state.PendingTBRequest.ID
			result.CanAcceptTBRequest = true
			result.CanRejectTBRequest = true
			result.AllowedActions = append(result.AllowedActions, ActionRespondTBRequest)
		}
	}
	return result
}

func analyzeReferee(state State, now time.Time) ActorAnalysis {
	result := ActorAnalysis{AllowedActions: []Action{}, BanPoolSlotIDs: []string{}, LegalPlacements: []PlacementOption{}, ShiroCells: []Cell{}, RobberyPlans: []RobberyPlan{}, TBRequestTeams: []TeamSide{}, TBResponseTeams: []TeamSide{}}
	switch state.Lifecycle {
	case LifecycleReady:
		result.AllowedActions = append(result.AllowedActions, ActionStartMatch)
		return result
	case LifecycleSuspended:
		result.AllowedActions = append(result.AllowedActions, ActionResumeMatch, ActionAbortMatch, ActionRecordSurrender)
		if state.Phase == PhaseBan || state.Phase == PhasePick {
			result.AllowedActions = append(result.AllowedActions, ActionSkipCurrentAction)
		}
		return result
	case LifecycleRunning:
		result.AllowedActions = append(result.AllowedActions, ActionSuspendMatch, ActionAbortMatch, ActionRecordSurrender)
	default:
		return result
	}

	if state.Timer.Paused {
		result.AllowedActions = append(result.AllowedActions, ActionResumeTimer, ActionCalibrateTimer)
	} else if state.Timer.Duration > 0 {
		result.AllowedActions = append(result.AllowedActions, ActionPauseTimer, ActionCalibrateTimer)
		if state.Timer.expired(now) {
			if (state.Phase == PhaseBan || state.Phase == PhasePick || state.Phase == PhaseWaitingForResult) && state.ActiveTeam.valid() && !state.TeamPauseUsed[state.ActiveTeam] {
				result.AllowedActions = append(result.AllowedActions, ActionGrantAdditionalTime)
			}
			if state.Phase == PhaseBan || state.Phase == PhasePick {
				result.AllowedActions = append(result.AllowedActions, ActionSkipCurrentAction)
			}
		}
	}
	switch state.Phase {
	case PhaseBan:
		if state.Timer.Paused {
			break
		}
		for id, slot := range state.PoolSlots {
			if slot.State == PoolSlotAvailable && slot.Mod != ModShiro && slot.Mod != ModTB {
				result.BanPoolSlotIDs = append(result.BanPoolSlotIDs, id)
			}
		}
		sort.Strings(result.BanPoolSlotIDs)
		if len(result.BanPoolSlotIDs) > 0 {
			result.AllowedActions = append(result.AllowedActions, ActionBanPoolSlot)
		}
	case PhasePick:
		if state.Timer.Paused {
			break
		}
		base := Analyze(state)
		for _, placement := range base.LegalPlacements {
			if state.PoolSlots[placement.PoolSlotID].Mod != ModShiro {
				result.LegalPlacements = append(result.LegalPlacements, placement)
			}
		}
		if len(result.LegalPlacements) > 0 {
			result.AllowedActions = append(result.AllowedActions, ActionPlacePiece)
		}
		if shiroAvailable(state) && len(base.EmptyCells) > 0 {
			result.AllowedActions = append(result.AllowedActions, ActionPlaceShiro)
			result.ShiroCells = append(result.ShiroCells, base.EmptyCells...)
		}
		for _, side := range []TeamSide{TeamRed, TeamBlue} {
			result.RobberyPlans = append(result.RobberyPlans, robberyPlans(state, side)...)
		}
		if len(result.RobberyPlans) > 0 {
			result.AllowedActions = append(result.AllowedActions, ActionRobPiece)
		}
		if !state.Timer.Paused && state.Turn >= 11 && state.Turn <= 14 {
			if state.PendingTBRequest == nil {
				result.AllowedActions = append(result.AllowedActions, ActionRequestTB)
				result.TBRequestTeams = append(result.TBRequestTeams, TeamRed, TeamBlue)
			} else {
				result.AllowedActions = append(result.AllowedActions, ActionRespondTBRequest)
				result.PendingTBRequestID = state.PendingTBRequest.ID
				result.CanAcceptTBRequest = true
				result.CanRejectTBRequest = true
				result.TBResponseTeams = append(result.TBResponseTeams, state.PendingTBRequest.RequestedBy.opponent())
			}
		}
	case PhaseWaitingForResult:
		result.AllowedActions = append(result.AllowedActions, ActionConfirmResult)
	case PhaseTBPreparation:
		if !state.Timer.Paused {
			result.AllowedActions = append(result.AllowedActions, ActionStartTB)
		}
	case PhaseTBPlaying:
		result.AllowedActions = append(result.AllowedActions, ActionConfirmTBResult)
	}
	return result
}

func shiroAvailable(state State) bool {
	for _, slot := range state.PoolSlots {
		if slot.Mod == ModShiro && slot.State == PoolSlotAvailable {
			return true
		}
	}
	return false
}

func robberyPlans(state State, team TeamSide) []RobberyPlan {
	if state.Lifecycle != LifecycleRunning || state.Phase != PhasePick || state.ActiveTeam != team {
		return []RobberyPlan{}
	}
	twos := state.Board.FindAlignments(team, 2)
	threes := state.Board.FindAlignments(team, 3)
	plans := make([]RobberyPlan, 0)
	for _, target := range state.Board.Pieces() {
		targetIsShiro := target.Mod == ModShiro && target.Outcome == OutcomeWhite && target.Owner == nil
		targetIsOpponent := target.Outcome == OutcomeWon && target.Owner != nil && *target.Owner == team.opponent()
		if !targetIsShiro && !targetIsOpponent {
			continue
		}
		if targetIsShiro {
			for _, alignment := range twos {
				sets := [][]string{append([]string(nil), alignment.BoardPieceIDs...)}
				if next, ok := boardAfterRobbery(state.Board, team, target.ID, sets[0], 2); ok && next.containsPieceID(target.ID) {
					plans = append(plans, RobberyPlan{TargetPieceID: target.ID, SacrificeSets: sets})
				}
			}
			continue
		}
		for _, alignment := range threes {
			ids := append([]string(nil), alignment.BoardPieceIDs...)
			if _, ok := boardAfterRobbery(state.Board, team, target.ID, ids, 3); ok {
				plans = append(plans, RobberyPlan{TargetPieceID: target.ID, SacrificeSets: [][]string{ids}})
			}
		}
		for first := range twos {
			for second := first + 1; second < len(twos); second++ {
				sets := [][]string{append([]string(nil), twos[first].BoardPieceIDs...), append([]string(nil), twos[second].BoardPieceIDs...)}
				ids, overlap := flattenSacrificeSets(sets)
				if overlap {
					continue
				}
				if _, ok := boardAfterRobbery(state.Board, team, target.ID, ids, 3); ok {
					plans = append(plans, RobberyPlan{TargetPieceID: target.ID, SacrificeSets: sets})
				}
			}
		}
	}
	sort.Slice(plans, func(i, j int) bool {
		left, right := plans[i], plans[j]
		if left.TargetPieceID != right.TargetPieceID {
			return left.TargetPieceID < right.TargetPieceID
		}
		return sacrificeKey(left.SacrificeSets) < sacrificeKey(right.SacrificeSets)
	})
	return plans
}

func sacrificeKey(sets [][]string) string {
	key := ""
	for _, set := range sets {
		ids := append([]string(nil), set...)
		sort.Strings(ids)
		key += "|"
		for _, id := range ids {
			key += id + ","
		}
	}
	return key
}
