package matchengine

import (
	"strings"
	"time"
)

// Execute applies one command to an independent copy of state. Failed commands
// never mutate the caller's aggregate.
func Execute(state State, actor Actor, command Command, now time.Time) (Transition, error) {
	next := state.Clone()

	var (
		events []Event
		err    error
	)
	switch typed := command.(type) {
	case StartMatch:
		events, err = startMatch(&next, actor, now)
	case BanPoolSlot:
		events, err = banPoolSlot(&next, actor, typed, now)
	case RefereeBanPoolSlot:
		events, err = refereeBanPoolSlot(&next, actor, typed, now)
	case PlacePiece:
		events, err = placePiece(&next, actor, typed, now)
	case RefereePlacePiece:
		events, err = refereePlacePiece(&next, actor, typed, now)
	case PlaceShiro:
		events, err = placeShiro(&next, actor, typed, now)
	case RefereePlaceShiro:
		events, err = refereePlaceShiro(&next, actor, typed, now)
	case RobPiece:
		events, err = robPiece(&next, actor, typed, now)
	case RefereeRobPiece:
		events, err = refereeRobPiece(&next, actor, typed, now)
	case GrantAdditionalTime:
		events, err = grantAdditionalTime(&next, actor, typed, now)
	case CalibrateTimer:
		events, err = calibrateTimer(&next, actor, typed, now)
	case PauseTimer:
		events, err = pauseTimer(&next, actor, typed, now)
	case ResumeTimer:
		events, err = resumeTimer(&next, actor, typed, now)
	case SuspendMatch:
		events, err = suspendMatch(&next, actor, typed, now)
	case ResumeMatch:
		events, err = resumeMatch(&next, actor, typed, now)
	case SkipCurrentAction:
		events, err = skipCurrentAction(&next, actor, typed, now)
	case AbortMatch:
		events, err = abortMatch(&next, actor, typed)
	case RequestTB:
		events, err = requestTB(&next, actor, typed, now)
	case RefereeRequestTB:
		events, err = refereeRequestTB(&next, actor, typed, now)
	case RespondTBRequest:
		events, err = respondTBRequest(&next, actor, typed, now)
	case RefereeRespondTBRequest:
		events, err = refereeRespondTBRequest(&next, actor, typed, now)
	case StartTB:
		events, err = startTB(&next, actor, typed, now)
	case ConfirmTBResult:
		events, err = confirmTBResult(&next, actor, typed)
	case RecordSurrender:
		events, err = recordSurrender(&next, actor, typed)
	case ConfirmBeatmapResult:
		events, err = confirmBeatmapResult(&next, actor, typed, now)
	default:
		err = ruleError(CodeInvalidRequest, "unsupported command")
	}
	if err != nil {
		return Transition{}, err
	}
	next.Version++
	return Transition{State: next, Events: events}, nil
}

func startMatch(state *State, actor Actor, now time.Time) ([]Event, error) {
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can start a match")
	}
	if state.Lifecycle != LifecycleReady {
		return nil, ruleError(CodeMatchLifecycleConflict, "match is not ready")
	}

	state.Lifecycle = LifecycleRunning
	state.Phase = PhaseBan
	state.Turn = firstBanTurn
	state.ActiveTeam = state.FirstBan
	state.Timer = Timer{StartedAt: now, Duration: state.Timers.Ban}
	return []Event{
		{Type: EventMatchStarted},
		{Type: EventBanPhaseStarted, Team: state.ActiveTeam},
		{Type: EventTimerStarted},
	}, nil
}

func banPoolSlot(state *State, actor Actor, command BanPoolSlot, now time.Time) ([]Event, error) {
	if err := requireRunningPhase(*state, PhaseBan); err != nil {
		return nil, err
	}
	if err := requireActiveStrategist(*state, actor); err != nil {
		return nil, err
	}
	if err := requireStrategistTimer(state.Timer, now); err != nil {
		return nil, err
	}

	slot, ok := state.PoolSlots[command.PoolSlotID]
	if !ok {
		return nil, ruleError(CodeInvalidPoolSlot, "pool slot does not exist")
	}
	if slot.State != PoolSlotAvailable || slot.Mod == ModShiro || slot.Mod == ModTB {
		return nil, ruleError(CodePoolSlotUnavailable, "pool slot cannot be banned")
	}

	slot.State = PoolSlotBanned
	state.PoolSlots[slot.ID] = slot
	events := []Event{{Type: EventPoolSlotBanned, Team: state.ActiveTeam, PoolSlotID: slot.ID}}

	state.Turn++
	if state.Turn > finalBanTurn {
		state.Turn = firstPickTurn
		state.ActiveTeam = state.FirstPick
		events = append(events,
			Event{Type: EventTurnAdvanced, Team: state.ActiveTeam},
			Event{Type: EventPickPhaseStarted, Team: state.ActiveTeam},
		)
		events = append(events, enterPickOrTerminal(state, now)...)
		return events, nil
	}

	switch state.Turn {
	case secondBanTurn, thirdBanTurn:
		state.ActiveTeam = state.FirstBan.opponent()
	case finalBanTurn:
		state.ActiveTeam = state.FirstBan
	}
	state.Timer = Timer{StartedAt: now, Duration: state.Timers.Ban}
	events = append(events,
		Event{Type: EventTurnAdvanced, Team: state.ActiveTeam},
		Event{Type: EventTimerStarted},
	)
	return events, nil
}

func refereeBanPoolSlot(state *State, actor Actor, command RefereeBanPoolSlot, now time.Time) ([]Event, error) {
	proxyActor, effectiveNow, err := refereeProxyContext(*state, actor, command.ActingTeam, command.Reason, now)
	if err != nil {
		return nil, err
	}
	events, err := banPoolSlot(state, proxyActor, BanPoolSlot{PoolSlotID: command.PoolSlotID}, effectiveNow)
	return appendRefereeProxyEvent(events, command.ActingTeam, command.Reason, err)
}

func placePiece(state *State, actor Actor, command PlacePiece, now time.Time) ([]Event, error) {
	if err := requireRunningPhase(*state, PhasePick); err != nil {
		return nil, err
	}
	if err := requireActiveStrategist(*state, actor); err != nil {
		return nil, err
	}
	if err := requireStrategistTimer(state.Timer, now); err != nil {
		return nil, err
	}
	if command.PieceID == "" || state.Board.containsPieceID(command.PieceID) {
		return nil, ruleError(CodeInvalidRequest, "board piece id must be non-empty and unique")
	}

	slot, ok := state.PoolSlots[command.PoolSlotID]
	if !ok {
		return nil, ruleError(CodeInvalidPoolSlot, "pool slot does not exist")
	}
	if slot.State != PoolSlotAvailable || slot.Mod == ModShiro || slot.Mod == ModTB {
		return nil, ruleError(CodePoolSlotUnavailable, "pool slot cannot be placed by this command")
	}
	zone, ok := state.Board.ZoneAt(command.Cell)
	if !ok || !state.Board.empty(command.Cell) {
		return nil, ruleError(CodeInvalidBoardCell, "board cell is invalid or occupied")
	}
	forceMod, err := placementForceMod(slot.Mod, zone)
	if err != nil {
		return nil, err
	}

	piece := BoardPiece{
		ID:               command.PieceID,
		SourcePoolSlotID: slot.ID,
		Mod:              slot.Mod,
		ForceMod:         forceMod,
		SelectedBy:       state.ActiveTeam,
		Outcome:          OutcomeWaitingResult,
	}
	state.Board.place(command.Cell, piece)
	slot.State = PoolSlotSelected
	state.PoolSlots[slot.ID] = slot
	state.Phase = PhaseWaitingForResult
	state.PendingPieceID = piece.ID
	state.Timer = Timer{StartedAt: now, Duration: state.Timers.ResultConfirmation}

	return []Event{
		{Type: EventPiecePlaced, Team: state.ActiveTeam, PoolSlotID: slot.ID, BoardPieceID: piece.ID, Cell: command.Cell},
		{Type: EventResultConfirmationRequested, BoardPieceID: piece.ID},
		{Type: EventTimerStarted},
	}, nil
}

func refereePlacePiece(state *State, actor Actor, command RefereePlacePiece, now time.Time) ([]Event, error) {
	proxyActor, effectiveNow, err := refereeProxyContext(*state, actor, command.ActingTeam, command.Reason, now)
	if err != nil {
		return nil, err
	}
	events, err := placePiece(state, proxyActor, PlacePiece{
		PoolSlotID: command.PoolSlotID, PieceID: command.PieceID, Cell: command.Cell,
	}, effectiveNow)
	return appendRefereeProxyEvent(events, command.ActingTeam, command.Reason, err)
}

func placeShiro(state *State, actor Actor, command PlaceShiro, now time.Time) ([]Event, error) {
	if err := requireRunningPhase(*state, PhasePick); err != nil {
		return nil, err
	}
	if err := requireActiveStrategist(*state, actor); err != nil {
		return nil, err
	}
	if err := requireStrategistTimer(state.Timer, now); err != nil {
		return nil, err
	}
	if command.PieceID == "" || state.Board.containsPieceID(command.PieceID) {
		return nil, ruleError(CodeInvalidRequest, "board piece id must be non-empty and unique")
	}
	if !state.Board.empty(command.Cell) {
		return nil, ruleError(CodeInvalidBoardCell, "board cell is invalid or occupied")
	}

	var shiro PoolSlot
	found := false
	for _, slot := range state.PoolSlots {
		if slot.Mod == ModShiro {
			shiro = slot
			found = true
			break
		}
	}
	if !found || shiro.State != PoolSlotAvailable {
		return nil, ruleError(CodePoolSlotUnavailable, "Shiro is not available")
	}

	piece := BoardPiece{
		ID:               command.PieceID,
		SourcePoolSlotID: shiro.ID,
		Mod:              ModShiro,
		SelectedBy:       state.ActiveTeam,
		Outcome:          OutcomeWhite,
	}
	state.Board.place(command.Cell, piece)
	shiro.State = PoolSlotSelected
	state.PoolSlots[shiro.ID] = shiro
	state.Turn++
	state.ActiveTeam = pickTeam(state.FirstPick, state.Turn)

	events := []Event{
		{Type: EventShiroPlaced, Team: piece.SelectedBy, PoolSlotID: shiro.ID, BoardPieceID: piece.ID, Cell: command.Cell},
		{Type: EventTurnAdvanced, Team: state.ActiveTeam},
	}
	events = append(events, enterPickOrTerminal(state, now)...)
	return events, nil
}

func refereePlaceShiro(state *State, actor Actor, command RefereePlaceShiro, now time.Time) ([]Event, error) {
	proxyActor, effectiveNow, err := refereeProxyContext(*state, actor, command.ActingTeam, command.Reason, now)
	if err != nil {
		return nil, err
	}
	events, err := placeShiro(state, proxyActor, PlaceShiro{PieceID: command.PieceID, Cell: command.Cell}, effectiveNow)
	return appendRefereeProxyEvent(events, command.ActingTeam, command.Reason, err)
}

func robPiece(state *State, actor Actor, command RobPiece, now time.Time) ([]Event, error) {
	if err := requireRunningPhase(*state, PhasePick); err != nil {
		return nil, err
	}
	if err := requireActiveStrategist(*state, actor); err != nil {
		return nil, err
	}
	if err := requireStrategistTimer(state.Timer, now); err != nil {
		return nil, err
	}
	team := state.ActiveTeam

	_, target, ok := state.Board.pieceByID(command.TargetPieceID)
	if !ok {
		return nil, ruleError(CodeRobberyRequirementsNotMet, "target piece does not exist")
	}
	targetIsShiro := target.Mod == ModShiro && target.Outcome == OutcomeWhite && target.Owner == nil
	targetIsOpponent := target.Outcome == OutcomeWon && target.Owner != nil && *target.Owner == team.opponent()
	if !targetIsShiro && !targetIsOpponent {
		return nil, ruleError(CodeRobberyRequirementsNotMet, "target is not an opponent WON piece or unowned Shiro")
	}

	sacrificeIDs, overlap := flattenSacrificeSets(command.SacrificeSets)
	if overlap {
		return nil, ruleError(CodeAlignmentOverlap, "a sacrifice piece appears more than once")
	}
	if targetIsShiro {
		if len(command.SacrificeSets) != 1 || len(command.SacrificeSets[0]) != 2 ||
			!state.Board.isAlignment(team, command.SacrificeSets[0], 2) {
			return nil, ruleError(CodeRobberyRequirementsNotMet, "Shiro robbery requires one own two-alignment")
		}
	} else if !validNormalRobberySacrifice(state.Board, team, command.SacrificeSets) {
		return nil, ruleError(CodeRobberyRequirementsNotMet, "opponent robbery requires one three-alignment or two distinct two-alignments")
	}

	requiredAlignmentLength := 3
	if targetIsShiro {
		requiredAlignmentLength = 2
	}
	nextBoard, valid := boardAfterRobbery(state.Board, team, target.ID, sacrificeIDs, requiredAlignmentLength)
	if !valid {
		return nil, ruleError(CodeRobberyRequirementsNotMet, "robbed target does not participate in the required resulting alignment")
	}
	state.Board = nextBoard
	if state.RobberyUsed == nil {
		state.RobberyUsed = make(map[TeamSide]bool, 2)
	}
	state.RobberyUsed[team] = true
	events := []Event{
		{Type: EventPiecesSacrificed, Team: team, BoardPieceIDs: append([]string(nil), sacrificeIDs...)},
		{Type: EventPieceRobbed, Team: team, BoardPieceID: target.ID},
	}
	if state.Board.hasFour(team) {
		winner := team
		finishMatch(state, winner, Result{Winner: winner, Reason: ResultReasonFourAlignment})
		events = append(events, Event{Type: EventMatchFinished, Team: winner})
		return events, nil
	}
	if shouldForceTB(*state) {
		events = append(events, startForcedTBPreparation(state, now)...)
	}
	return events, nil
}

func refereeRobPiece(state *State, actor Actor, command RefereeRobPiece, now time.Time) ([]Event, error) {
	proxyActor, effectiveNow, err := refereeProxyContext(*state, actor, command.ActingTeam, command.Reason, now)
	if err != nil {
		return nil, err
	}
	events, err := robPiece(state, proxyActor, RobPiece{
		TargetPieceID: command.TargetPieceID, SacrificeSets: command.SacrificeSets,
	}, effectiveNow)
	return appendRefereeProxyEvent(events, command.ActingTeam, command.Reason, err)
}

func flattenSacrificeSets(sets [][]string) ([]string, bool) {
	seen := make(map[string]struct{})
	var flattened []string
	for _, set := range sets {
		for _, pieceID := range set {
			if _, exists := seen[pieceID]; exists {
				return nil, true
			}
			seen[pieceID] = struct{}{}
			flattened = append(flattened, pieceID)
		}
	}
	return flattened, false
}

func validNormalRobberySacrifice(board Board, team TeamSide, sets [][]string) bool {
	if len(sets) == 1 {
		return board.isAlignment(team, sets[0], 3)
	}
	if len(sets) == 2 {
		return board.isAlignment(team, sets[0], 2) && board.isAlignment(team, sets[1], 2)
	}
	return false
}

func boardAfterRobbery(board Board, team TeamSide, targetID string, sacrificeIDs []string, alignmentLength int) (Board, bool) {
	next := board.Clone()
	next.markDead(sacrificeIDs)
	if !next.setOwner(targetID, team) || !next.pieceParticipatesInAlignment(team, targetID, alignmentLength) {
		return Board{}, false
	}
	return next, true
}

// teamHasLegalRobbery exhaustively checks the small 4x4 board using the same
// sacrifice and resulting-alignment rules as RobPiece.
func teamHasLegalRobbery(board Board, team TeamSide) bool {
	twoAlignments := board.FindAlignments(team, 2)
	threeAlignments := board.FindAlignments(team, 3)
	for _, target := range board.pieces {
		targetIsShiro := target.Mod == ModShiro && target.Outcome == OutcomeWhite && target.Owner == nil
		targetIsOpponent := target.Outcome == OutcomeWon && target.Owner != nil && *target.Owner == team.opponent()
		switch {
		case targetIsShiro:
			for _, sacrifice := range twoAlignments {
				if _, valid := boardAfterRobbery(board, team, target.ID, sacrifice.BoardPieceIDs, 2); valid {
					return true
				}
			}
		case targetIsOpponent:
			for _, sacrifice := range threeAlignments {
				if _, valid := boardAfterRobbery(board, team, target.ID, sacrifice.BoardPieceIDs, 3); valid {
					return true
				}
			}
			for first := 0; first < len(twoAlignments); first++ {
				for second := first + 1; second < len(twoAlignments); second++ {
					sacrificeIDs, overlap := flattenSacrificeSets([][]string{
						twoAlignments[first].BoardPieceIDs,
						twoAlignments[second].BoardPieceIDs,
					})
					if overlap {
						continue
					}
					if _, valid := boardAfterRobbery(board, team, target.ID, sacrificeIDs, 3); valid {
						return true
					}
				}
			}
		}
	}
	return false
}

func grantAdditionalTime(state *State, actor Actor, command GrantAdditionalTime, now time.Time) ([]Event, error) {
	if state.Lifecycle != LifecycleRunning {
		return nil, ruleError(CodeMatchLifecycleConflict, "match is not running")
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can grant additional time")
	}
	if missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "additional-time reason is required")
	}
	if state.Phase != PhaseBan && state.Phase != PhasePick && state.Phase != PhaseWaitingForResult {
		return nil, ruleError(CodeActionNotAllowed, "additional time is not available for this phase")
	}
	if !state.ActiveTeam.valid() {
		return nil, ruleError(CodeActionNotAllowed, "team-action timer has no active team")
	}
	if !state.Timer.expired(now) {
		return nil, ruleError(CodeActionNotAllowed, "team-action timer has not expired")
	}
	if state.TeamPauseUsed[state.ActiveTeam] {
		return nil, ruleError(CodeTeamPauseAlreadyUsed, "active team already used its pause opportunity")
	}

	duration := state.Timers.PickAdditional
	if state.Phase == PhaseBan {
		duration = state.Timers.BanAdditional
	} else if state.Phase == PhaseWaitingForResult {
		duration = state.Timers.ResultConfirmationAdditional
	}
	team := state.ActiveTeam
	if state.TeamPauseUsed == nil {
		state.TeamPauseUsed = make(map[TeamSide]bool, 2)
	}
	state.TeamPauseUsed[team] = true
	state.Timer = Timer{StartedAt: now, Duration: duration}
	return []Event{
		{Type: EventAdditionalTimeGranted, Team: team, Duration: duration, Reason: command.Reason},
		{Type: EventTimerStarted},
	}, nil
}

func calibrateTimer(state *State, actor Actor, command CalibrateTimer, now time.Time) ([]Event, error) {
	if state.Lifecycle != LifecycleRunning || state.Phase == PhaseNone {
		return nil, ruleError(CodeMatchLifecycleConflict, "match has no active phase")
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can calibrate a timer")
	}
	if missingReason(command.Reason) || command.Remaining < 0 {
		return nil, ruleError(CodeInvalidRequest, "non-negative remaining time and reason are required")
	}
	if state.Timer.Duration <= 0 && !state.Timer.Paused {
		return nil, ruleError(CodeActionNotAllowed, "current phase has no timer")
	}
	if state.Timer.Paused {
		state.Timer.RemainingAtPause = command.Remaining
	} else {
		state.Timer = Timer{StartedAt: now, Duration: command.Remaining}
	}
	return []Event{{Type: EventTimerCalibrated, Duration: command.Remaining, Reason: command.Reason}}, nil
}

func pauseTimer(state *State, actor Actor, command PauseTimer, now time.Time) ([]Event, error) {
	if state.Lifecycle != LifecycleRunning || state.Phase == PhaseNone {
		return nil, ruleError(CodeMatchLifecycleConflict, "match has no active timer")
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can pause a timer")
	}
	if missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "timer pause reason is required")
	}
	if state.Timer.Duration <= 0 && !state.Timer.Paused {
		return nil, ruleError(CodeActionNotAllowed, "current phase has no timer")
	}
	if state.Timer.Paused {
		return nil, ruleError(CodeActionNotAllowed, "timer is already paused")
	}
	state.Timer.pause(now)
	return []Event{{Type: EventTimerPaused, Reason: command.Reason}}, nil
}

func resumeTimer(state *State, actor Actor, command ResumeTimer, now time.Time) ([]Event, error) {
	if state.Lifecycle != LifecycleRunning || state.Phase == PhaseNone {
		return nil, ruleError(CodeMatchLifecycleConflict, "match has no active timer")
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can resume a timer")
	}
	if missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "timer resume reason is required")
	}
	if !state.Timer.Paused {
		return nil, ruleError(CodeActionNotAllowed, "timer is not paused")
	}
	state.Timer.resume(now)
	return []Event{{Type: EventTimerResumed, Reason: command.Reason}}, nil
}

func suspendMatch(state *State, actor Actor, command SuspendMatch, now time.Time) ([]Event, error) {
	if state.Lifecycle != LifecycleRunning {
		return nil, ruleError(CodeMatchLifecycleConflict, "only a running match can be suspended")
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can suspend a match")
	}
	if missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "suspension reason is required")
	}

	hadTimer := state.Timer.Duration > 0 || state.Timer.Paused
	wasPaused := state.Timer.Paused
	state.Suspension = &SuspensionState{
		Reason: command.Reason, SuspendedAt: now, HadTimer: hadTimer, TimerWasPaused: wasPaused,
	}
	state.Lifecycle = LifecycleSuspended
	events := []Event{{Type: EventMatchSuspended, Reason: command.Reason}}
	if hadTimer && !wasPaused {
		state.Timer.pause(now)
		events = append(events, Event{Type: EventTimerPaused, Reason: command.Reason})
	}
	return events, nil
}

func resumeMatch(state *State, actor Actor, command ResumeMatch, now time.Time) ([]Event, error) {
	if state.Lifecycle != LifecycleSuspended || state.Suspension == nil {
		return nil, ruleError(CodeMatchLifecycleConflict, "match is not suspended")
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can resume a match")
	}
	if missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "resume reason is required")
	}

	suspension := *state.Suspension
	state.Suspension = nil
	state.Lifecycle = LifecycleRunning
	events := []Event{{Type: EventMatchResumed, Reason: command.Reason}}
	if suspension.HadTimer && !suspension.TimerWasPaused {
		state.Timer.resume(now)
		events = append(events, Event{Type: EventTimerResumed, Reason: command.Reason})
	}
	return events, nil
}

func skipCurrentAction(state *State, actor Actor, command SkipCurrentAction, now time.Time) ([]Event, error) {
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can skip an action")
	}
	if missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "skip reason is required")
	}
	if state.Lifecycle != LifecycleRunning && state.Lifecycle != LifecycleSuspended {
		return nil, ruleError(CodeMatchLifecycleConflict, "match action cannot be skipped")
	}
	if state.Phase == PhaseWaitingForResult {
		return nil, ruleError(CodeResultNotPending, "a played beatmap result cannot be skipped")
	}
	if state.Phase != PhaseBan && state.Phase != PhasePick {
		return nil, ruleError(CodeActionNotAllowed, "current action has no defined skip transition")
	}
	wasSuspended := state.Lifecycle == LifecycleSuspended
	if !wasSuspended && !state.Timer.expired(now) {
		return nil, ruleError(CodeActionNotAllowed, "active timer has not expired")
	}
	state.Lifecycle = LifecycleRunning
	state.Suspension = nil
	events := []Event{{Type: EventActionSkipped, Team: state.ActiveTeam, Reason: command.Reason}}

	if state.Phase == PhaseBan {
		state.Turn++
		if state.Turn > finalBanTurn {
			state.Turn = firstPickTurn
			state.ActiveTeam = state.FirstPick
			events = append(events,
				Event{Type: EventTurnAdvanced, Team: state.ActiveTeam},
				Event{Type: EventPickPhaseStarted, Team: state.ActiveTeam},
			)
			events = append(events, enterPickOrTerminal(state, now)...)
		} else {
			switch state.Turn {
			case secondBanTurn, thirdBanTurn:
				state.ActiveTeam = state.FirstBan.opponent()
			case finalBanTurn:
				state.ActiveTeam = state.FirstBan
			}
			state.Timer = Timer{StartedAt: now, Duration: state.Timers.Ban}
			events = append(events, Event{Type: EventTurnAdvanced, Team: state.ActiveTeam}, Event{Type: EventTimerStarted})
		}
	} else {
		state.Turn++
		state.ActiveTeam = pickTeam(state.FirstPick, state.Turn)
		events = append(events, Event{Type: EventTurnAdvanced, Team: state.ActiveTeam})
		events = append(events, enterPickOrTerminal(state, now)...)
	}

	if wasSuspended && state.Lifecycle == LifecycleRunning {
		state.Lifecycle = LifecycleSuspended
		state.Timer.pause(now)
		state.Suspension = &SuspensionState{
			Reason: command.Reason, SuspendedAt: now, HadTimer: true, TimerWasPaused: false,
		}
		events = append(events, Event{Type: EventTimerPaused, Reason: command.Reason})
	}
	return events, nil
}

func abortMatch(state *State, actor Actor, command AbortMatch) ([]Event, error) {
	if state.Lifecycle != LifecycleRunning && state.Lifecycle != LifecycleSuspended {
		return nil, ruleError(CodeMatchLifecycleConflict, "match cannot be aborted")
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can abort a match")
	}
	if missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "abort reason is required")
	}
	state.Lifecycle = LifecycleAborted
	state.Phase = PhaseNone
	state.ActiveTeam = ""
	state.PendingPieceID = ""
	state.PendingTBRequest = nil
	state.Suspension = nil
	state.Timer = Timer{}
	state.AbortReason = command.Reason
	return []Event{{Type: EventMatchAborted, Reason: command.Reason}}, nil
}

func requestTB(state *State, actor Actor, command RequestTB, now time.Time) ([]Event, error) {
	if err := requireRunningPhase(*state, PhasePick); err != nil {
		return nil, err
	}
	if actor.Capability != CapabilityCaptain || actor.Team == nil || !actor.Team.valid() {
		return nil, ruleError(CodeActionNotAllowed, "a team captain is required")
	}
	if state.Timer.Paused {
		return nil, ruleError(CodeTimerPaused, "TB agreement is unavailable while the timer is paused")
	}
	if command.RequestID == "" {
		return nil, ruleError(CodeInvalidRequest, "TB request id is required")
	}
	if state.PendingTBRequest != nil {
		return nil, ruleError(CodeTBNotAvailable, "a TB request is already pending")
	}
	available := command.Basis == TBBasisCaptainAgreement && state.Turn >= 11 && state.Turn <= 14
	if !available {
		return nil, ruleError(CodeTBNotAvailable, "requested TB basis is not currently available")
	}

	state.PendingTBRequest = &TBRequestState{
		ID:          command.RequestID,
		RequestedBy: *actor.Team,
		Basis:       command.Basis,
	}
	return []Event{{Type: EventTBRequested, Team: *actor.Team, RequestID: command.RequestID, Basis: command.Basis}}, nil
}

func refereeRequestTB(state *State, actor Actor, command RefereeRequestTB, now time.Time) ([]Event, error) {
	proxyActor, err := refereeProxyCaptainContext(actor, command.ActingTeam, command.Reason)
	if err != nil {
		return nil, err
	}
	events, err := requestTB(state, proxyActor, RequestTB{RequestID: command.RequestID, Basis: command.Basis}, now)
	return appendRefereeProxyEvent(events, command.ActingTeam, command.Reason, err)
}

func respondTBRequest(state *State, actor Actor, command RespondTBRequest, now time.Time) ([]Event, error) {
	if err := requireRunningPhase(*state, PhasePick); err != nil {
		return nil, err
	}
	if actor.Capability != CapabilityCaptain || actor.Team == nil || !actor.Team.valid() {
		return nil, ruleError(CodeActionNotAllowed, "a team captain is required")
	}
	if state.Timer.Paused {
		return nil, ruleError(CodeTimerPaused, "TB agreement is unavailable while the timer is paused")
	}
	if state.Turn < 11 || state.Turn > 14 {
		return nil, ruleError(CodeTBNotAvailable, "TB agreement is outside turns 11 through 14")
	}
	pending := state.PendingTBRequest
	if pending == nil || command.RequestID == "" || command.RequestID != pending.ID {
		return nil, ruleError(CodeTBNotAvailable, "matching TB request is not pending")
	}
	if *actor.Team == pending.RequestedBy {
		return nil, ruleError(CodeActionNotAllowed, "requesting team cannot respond to its own TB request")
	}

	requestID := pending.ID
	state.PendingTBRequest = nil
	if !command.Accept {
		return []Event{{Type: EventTBRequestRejected, Team: *actor.Team, RequestID: requestID, Basis: pending.Basis}}, nil
	}

	state.Phase = PhaseTBPreparation
	state.ActiveTeam = ""
	state.TBEntry = &TBEntryState{Basis: pending.Basis, RequestID: requestID, RequestedBy: pending.RequestedBy}
	state.Timer = Timer{StartedAt: now, Duration: state.Timers.TBPreparation}
	return []Event{
		{Type: EventTBRequestAccepted, Team: *actor.Team, RequestID: requestID, Basis: pending.Basis},
		{Type: EventTBPreparationStarted, RequestID: requestID, Basis: pending.Basis},
		{Type: EventTimerStarted},
	}, nil
}

func refereeRespondTBRequest(state *State, actor Actor, command RefereeRespondTBRequest, now time.Time) ([]Event, error) {
	proxyActor, err := refereeProxyCaptainContext(actor, command.ActingTeam, command.Reason)
	if err != nil {
		return nil, err
	}
	events, err := respondTBRequest(state, proxyActor, RespondTBRequest{RequestID: command.RequestID, Accept: command.Accept}, now)
	return appendRefereeProxyEvent(events, command.ActingTeam, command.Reason, err)
}

func refereeProxyCaptainContext(actor Actor, actingTeam TeamSide, reason string) (Actor, error) {
	if actor.Capability != CapabilityReferee {
		return Actor{}, ruleError(CodeActionNotAllowed, "only a referee can proxy a captain action")
	}
	if !actingTeam.valid() || missingReason(reason) {
		return Actor{}, ruleError(CodeInvalidRequest, "proxy acting team and reason are required")
	}
	return CaptainActor(actingTeam), nil
}

func refereeProxyContext(state State, actor Actor, actingTeam TeamSide, reason string, now time.Time) (Actor, time.Time, error) {
	if actor.Capability != CapabilityReferee {
		return Actor{}, time.Time{}, ruleError(CodeActionNotAllowed, "only a referee can proxy a strategist action")
	}
	if !actingTeam.valid() || missingReason(reason) {
		return Actor{}, time.Time{}, ruleError(CodeInvalidRequest, "proxy acting team and reason are required")
	}
	if !state.Timer.Paused && state.Timer.expired(now) {
		now = state.Timer.StartedAt
	}
	return StrategistActor(actingTeam), now, nil
}

func appendRefereeProxyEvent(events []Event, team TeamSide, reason string, err error) ([]Event, error) {
	if err != nil {
		return nil, err
	}
	return append(events, Event{Type: EventRefereeProxyActionRecorded, Team: team, Reason: reason}), nil
}

func startTB(state *State, actor Actor, command StartTB, now time.Time) ([]Event, error) {
	if err := requireRunningPhase(*state, PhaseTBPreparation); err != nil {
		return nil, err
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can start TB play")
	}
	if state.Timer.Paused {
		return nil, ruleError(CodeTimerPaused, "TB preparation timer is paused")
	}
	if state.Timer.expired(now) && missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "expired TB preparation requires a referee reason")
	}

	state.Phase = PhaseTBPlaying
	state.Timer = Timer{}
	return []Event{
		{Type: EventTBStarted, Reason: command.Reason},
		{Type: EventTimerStopped},
	}, nil
}

func confirmTBResult(state *State, actor Actor, command ConfirmTBResult) ([]Event, error) {
	if err := requireRunningPhase(*state, PhaseTBPlaying); err != nil {
		return nil, err
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can confirm a TB result")
	}
	if !command.WinningTeam.valid() {
		return nil, ruleError(CodeInvalidRequest, "winning team must be RED or BLUE")
	}
	finishMatch(state, command.WinningTeam, Result{Winner: command.WinningTeam, Reason: ResultReasonTB})
	return []Event{
		{Type: EventTBResultConfirmed, Team: command.WinningTeam},
		{Type: EventMatchFinished, Team: command.WinningTeam},
	}, nil
}

func recordSurrender(state *State, actor Actor, command RecordSurrender) ([]Event, error) {
	if state.Lifecycle != LifecycleRunning && state.Lifecycle != LifecycleSuspended {
		return nil, ruleError(CodeMatchLifecycleConflict, "match is not running")
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can record surrender")
	}
	if !command.SurrenderingTeam.valid() || missingReason(command.Reason) {
		return nil, ruleError(CodeInvalidRequest, "surrendering team and reason are required")
	}
	evidence, ok := validateSurrenderEvidence(state.Rosters[command.SurrenderingTeam], command.ConfirmingPlayerIDs)
	if !ok {
		return nil, ruleError(CodeSurrenderEvidenceInvalid, "surrender requires four rostered players including the leader")
	}

	winner := command.SurrenderingTeam.opponent()
	surrendering := command.SurrenderingTeam
	finishMatch(state, winner, Result{
		Winner:              winner,
		Reason:              ResultReasonSurrender,
		SurrenderingTeam:    &surrendering,
		ConfirmingPlayerIDs: evidence,
	})
	return []Event{
		{Type: EventSurrenderRecorded, Team: surrendering, PlayerIDs: append([]int64(nil), evidence...), Reason: command.Reason},
		{Type: EventMatchFinished, Team: winner},
	}, nil
}

func validateSurrenderEvidence(roster Roster, submitted []int64) ([]int64, bool) {
	rostered := make(map[int64]struct{}, len(roster.PlayerIDs))
	for _, playerID := range roster.PlayerIDs {
		rostered[playerID] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(submitted))
	unique := make([]int64, 0, len(submitted))
	leaderIncluded := false
	for _, playerID := range submitted {
		if _, ok := rostered[playerID]; !ok {
			return nil, false
		}
		if _, duplicate := seen[playerID]; duplicate {
			continue
		}
		seen[playerID] = struct{}{}
		unique = append(unique, playerID)
		leaderIncluded = leaderIncluded || playerID == roster.LeaderID
	}
	return unique, len(unique) >= 4 && leaderIncluded
}

func missingReason(reason string) bool {
	return strings.TrimSpace(reason) == ""
}

func finishMatch(state *State, winner TeamSide, result Result) {
	state.Winner = &winner
	state.Result = &result
	state.Lifecycle = LifecycleFinished
	state.Phase = PhaseNone
	state.ActiveTeam = ""
	state.PendingPieceID = ""
	state.PendingTBRequest = nil
	state.Stalemate = nil
	state.Suspension = nil
	state.AbortReason = ""
	state.Timer = Timer{}
}

func confirmBeatmapResult(state *State, actor Actor, command ConfirmBeatmapResult, now time.Time) ([]Event, error) {
	if err := requireRunningPhase(*state, PhaseWaitingForResult); err != nil {
		return nil, err
	}
	if actor.Capability != CapabilityReferee {
		return nil, ruleError(CodeActionNotAllowed, "only a referee can confirm a beatmap result")
	}
	if command.BoardPieceID == "" || command.BoardPieceID != state.PendingPieceID {
		return nil, ruleError(CodeResultNotPending, "board piece is not awaiting a result")
	}
	if !command.WinningTeam.valid() {
		return nil, ruleError(CodeInvalidRequest, "winning team must be RED or BLUE")
	}
	if !state.Board.setOwner(command.BoardPieceID, command.WinningTeam) {
		return nil, ruleError(CodeResultNotPending, "pending board piece is absent")
	}

	state.PendingPieceID = ""
	events := []Event{
		{Type: EventBeatmapResultConfirmed, Team: command.WinningTeam, BoardPieceID: command.BoardPieceID},
		{Type: EventPieceWon, Team: command.WinningTeam, BoardPieceID: command.BoardPieceID},
	}
	if state.Board.hasFour(command.WinningTeam) {
		winner := command.WinningTeam
		finishMatch(state, winner, Result{Winner: winner, Reason: ResultReasonFourAlignment})
		events = append(events, Event{Type: EventMatchFinished, Team: winner})
		return events, nil
	}

	state.Turn++
	state.ActiveTeam = pickTeam(state.FirstPick, state.Turn)
	events = append(events, Event{Type: EventTurnAdvanced, Team: state.ActiveTeam})
	events = append(events, enterPickOrTerminal(state, now)...)
	return events, nil
}

func enterPickOrTerminal(state *State, now time.Time) []Event {
	state.Phase = PhasePick
	var events []Event
	if state.Turn > 14 && state.PendingTBRequest != nil {
		pending := *state.PendingTBRequest
		state.PendingTBRequest = nil
		events = append(events, Event{
			Type: EventTBRequestExpired, Team: pending.RequestedBy, RequestID: pending.ID, Basis: pending.Basis,
		})
	}
	if shouldForceTB(*state) {
		return append(events, startForcedTBPreparation(state, now)...)
	}
	analysis := Analyze(*state)
	if !analysis.Stalemate {
		state.Timer = Timer{StartedAt: now, Duration: state.Timers.Pick}
		return append(events, Event{Type: EventTimerStarted})
	}

	redCount := analysis.WonCounts[TeamRed]
	blueCount := analysis.WonCounts[TeamBlue]
	state.Timer = Timer{}
	state.PendingTBRequest = nil
	events = append(events, Event{Type: EventStalemateDetected})
	if redCount == blueCount {
		state.Lifecycle = LifecycleAdjudicationRequired
		state.Phase = PhaseNone
		state.ActiveTeam = ""
		state.Stalemate = &StalemateEvidence{RedWonCount: redCount, BlueWonCount: blueCount}
		return append(events, Event{Type: EventAdjudicationRequired})
	}

	winner := TeamRed
	if blueCount > redCount {
		winner = TeamBlue
	}
	finishMatch(state, winner, Result{
		Winner: winner, Reason: ResultReasonStalemateWonCount,
		RedWonCount: redCount, BlueWonCount: blueCount,
	})
	return append(events, Event{Type: EventMatchFinished, Team: winner})
}

func shouldForceTB(state State) bool {
	return state.Lifecycle == LifecycleRunning && state.Phase == PhasePick && forcedTBRequirementsMet(state)
}

func forcedTBRequirementsMet(state State) bool {
	if state.Turn < 15 || state.Board.hasFour(TeamRed) || state.Board.hasFour(TeamBlue) {
		return false
	}
	redSatisfied := state.RobberyUsed[TeamRed] || !teamHasLegalRobbery(state.Board, TeamRed)
	blueSatisfied := state.RobberyUsed[TeamBlue] || !teamHasLegalRobbery(state.Board, TeamBlue)
	return redSatisfied && blueSatisfied
}

func startForcedTBPreparation(state *State, now time.Time) []Event {
	state.Phase = PhaseTBPreparation
	state.ActiveTeam = ""
	state.PendingTBRequest = nil
	state.TBEntry = &TBEntryState{Basis: TBBasisForcedAfterRobberyChecks}
	state.Timer = Timer{StartedAt: now, Duration: state.Timers.TBPreparation}
	return []Event{
		{Type: EventTBForced, Basis: TBBasisForcedAfterRobberyChecks},
		{Type: EventTBPreparationStarted, Basis: TBBasisForcedAfterRobberyChecks},
		{Type: EventTimerStarted},
	}
}

func requireRunningPhase(state State, phase Phase) error {
	if state.Lifecycle != LifecycleRunning {
		return ruleError(CodeMatchLifecycleConflict, "match is not running")
	}
	if state.Phase != phase {
		return ruleError(CodeMatchPhaseConflict, "command is invalid for the current phase")
	}
	return nil
}

func requireActiveStrategist(state State, actor Actor) error {
	if actor.Capability != CapabilityStrategist || actor.Team == nil || !actor.Team.valid() {
		return ruleError(CodeActionNotAllowed, "an identified strategist is required")
	}
	if *actor.Team != state.ActiveTeam {
		return ruleError(CodeNotActiveTeam, "strategist does not represent the active team")
	}
	return nil
}

func requireStrategistTimer(timer Timer, now time.Time) error {
	if timer.Paused {
		return ruleError(CodeTimerPaused, "team-action timer is paused")
	}
	if timer.expired(now) {
		return ruleError(CodeTimerExpired, "team-action timer expired")
	}
	return nil
}

func placementForceMod(mod Mod, zone Zone) (*ForceMod, error) {
	switch mod {
	case ModNM:
		return nil, nil
	case ModHD:
		if zone != ZoneHD {
			return nil, ruleError(CodeInvalidModZone, "HD requires an HD zone")
		}
		return nil, nil
	case ModHR:
		if zone != ZoneHR {
			return nil, ruleError(CodeInvalidModZone, "HR requires an HR zone")
		}
		return nil, nil
	case ModDT:
		if zone != ZoneDT {
			return nil, ruleError(CodeInvalidModZone, "DT requires a DT zone")
		}
		return nil, nil
	case ModFM:
		forceMod := ForceModNM
		switch zone {
		case ZoneHD:
			forceMod = ForceModHD
		case ZoneHR:
			forceMod = ForceModHR
		case ZoneDT:
			forceMod = ForceModNM
		}
		return &forceMod, nil
	default:
		return nil, ruleError(CodePoolSlotUnavailable, "slot mod cannot be placed as a normal beatmap")
	}
}

func pickTeam(first TeamSide, turn int) TeamSide {
	if turn%2 == 1 {
		return first
	}
	return first.opponent()
}
