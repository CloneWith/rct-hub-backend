package matchengine

import (
	"fmt"
	"strings"
)

// ValidateState checks the structural invariants required before a recovered
// State may be used as the authoritative aggregate. Command-specific legality
// remains the responsibility of Execute.
func ValidateState(state State) error {
	if !validLifecycle(state.Lifecycle) {
		return fmt.Errorf("invalid lifecycle %q", state.Lifecycle)
	}
	if !validPhase(state.Phase) {
		return fmt.Errorf("invalid phase %q", state.Phase)
	}
	if !state.FirstBan.valid() || !state.FirstPick.valid() {
		return fmt.Errorf("first ban and first pick teams must be RED or BLUE")
	}
	if _, err := validateAndCloneRosters(state.Rosters); err != nil {
		return fmt.Errorf("invalid rosters: %w", err)
	}
	if !state.Timers.valid() {
		return fmt.Errorf("invalid timer configuration")
	}
	if err := validateSideFlags("robbery used", state.RobberyUsed); err != nil {
		return err
	}
	if err := validateSideFlags("team pause used", state.TeamPauseUsed); err != nil {
		return err
	}
	if err := validatePoolAndBoard(state); err != nil {
		return err
	}
	if err := validateTurnAndTimer(state); err != nil {
		return err
	}
	if err := validateLifecycleState(state); err != nil {
		return err
	}
	return validateRecoveryEvidence(state)
}

func validateTurnAndTimer(state State) error {
	if err := validateTimerShape(state.Timer); err != nil {
		return err
	}

	requiresTimer := false
	switch state.Phase {
	case PhaseNone:
		switch state.Lifecycle {
		case LifecycleReady:
			if state.Turn != 0 {
				return fmt.Errorf("READY state must start at turn zero")
			}
		case LifecycleAdjudicationRequired:
			if state.Turn < 1 {
				return fmt.Errorf("ADJUDICATION_REQUIRED state has an invalid turn")
			}
		case LifecycleFinished, LifecycleAborted:
			if state.Turn < -3 {
				return fmt.Errorf("terminal state has an invalid turn")
			}
		}
	case PhaseBan:
		requiresTimer = true
		if state.Turn < -3 || state.Turn > 0 {
			return fmt.Errorf("ban phase has invalid turn %d", state.Turn)
		}
		expectedTeam := state.FirstBan
		if state.Turn == -2 || state.Turn == -1 {
			expectedTeam = state.FirstBan.opponent()
		}
		if state.ActiveTeam != expectedTeam {
			return fmt.Errorf("ban phase active team does not match turn %d", state.Turn)
		}
	case PhasePick, PhaseWaitingForResult:
		requiresTimer = true
		if state.Turn < 1 {
			return fmt.Errorf("phase %s has invalid turn %d", state.Phase, state.Turn)
		}
		if state.ActiveTeam != pickTeam(state.FirstPick, state.Turn) {
			return fmt.Errorf("phase %s active team does not match turn %d", state.Phase, state.Turn)
		}
	case PhaseTBPreparation:
		requiresTimer = true
		if state.Turn < 1 {
			return fmt.Errorf("TB preparation has an invalid turn")
		}
	case PhaseTBPlaying:
		if state.Turn < 1 {
			return fmt.Errorf("TB play has an invalid turn")
		}
	}

	if requiresTimer && state.Timer.Duration <= 0 {
		return fmt.Errorf("phase %s requires an active timer", state.Phase)
	}
	if !requiresTimer && state.Timer.Duration != 0 {
		return fmt.Errorf("phase %s cannot retain an active timer", state.Phase)
	}
	return nil
}

func validateTimerShape(timer Timer) error {
	if timer.Duration < 0 || timer.RemainingAtPause < 0 {
		return fmt.Errorf("timer durations cannot be negative")
	}
	if timer.Duration == 0 {
		if !timer.StartedAt.IsZero() || timer.Paused || timer.RemainingAtPause != 0 {
			return fmt.Errorf("empty timer contains active data")
		}
		return nil
	}
	if timer.StartedAt.IsZero() {
		return fmt.Errorf("active timer is missing its server-time anchor")
	}
	if timer.Paused {
		if timer.RemainingAtPause > timer.Duration {
			return fmt.Errorf("paused timer remaining duration exceeds its window")
		}
	} else if timer.RemainingAtPause != 0 {
		return fmt.Errorf("running timer contains paused remaining duration")
	}
	return nil
}

func validLifecycle(lifecycle Lifecycle) bool {
	switch lifecycle {
	case LifecycleReady, LifecycleRunning, LifecycleSuspended,
		LifecycleAdjudicationRequired, LifecycleFinished, LifecycleAborted:
		return true
	default:
		return false
	}
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhaseNone, PhaseBan, PhasePick, PhaseWaitingForResult, PhaseTBPreparation, PhaseTBPlaying:
		return true
	default:
		return false
	}
}

func validateSideFlags(name string, flags map[TeamSide]bool) error {
	if flags == nil {
		return fmt.Errorf("%s flags are missing", name)
	}
	if _, ok := flags[TeamRed]; !ok {
		return fmt.Errorf("%s RED flag is missing", name)
	}
	if _, ok := flags[TeamBlue]; !ok {
		return fmt.Errorf("%s BLUE flag is missing", name)
	}
	for side := range flags {
		if !side.valid() {
			return fmt.Errorf("%s contains invalid team %q", name, side)
		}
	}
	return nil
}

func validatePoolAndBoard(state State) error {
	if state.PoolSlots == nil {
		return fmt.Errorf("pool slots are missing")
	}
	shiroCount, tbCount := 0, 0
	for id, slot := range state.PoolSlots {
		if id == "" || slot.ID != id || !slot.Mod.valid() {
			return fmt.Errorf("invalid pool slot %q", id)
		}
		switch slot.State {
		case PoolSlotAvailable, PoolSlotBanned, PoolSlotSelected:
		default:
			return fmt.Errorf("pool slot %q has invalid state %q", id, slot.State)
		}
		if slot.Mod == ModShiro {
			shiroCount++
			if slot.State == PoolSlotBanned {
				return fmt.Errorf("shiro pool slot %q cannot be banned", id)
			}
		}
		if slot.Mod == ModTB {
			tbCount++
			if slot.State != PoolSlotAvailable {
				return fmt.Errorf("tiebreaker pool slot %q must remain available", id)
			}
		}
	}
	if shiroCount != 1 || tbCount != 1 {
		return fmt.Errorf("pool requires exactly one Shiro and one TB slot")
	}
	if state.Board.pieces == nil {
		return fmt.Errorf("board pieces are missing")
	}

	seenPieceIDs := make(map[string]struct{}, len(state.Board.pieces))
	seenPoolSlotIDs := make(map[string]struct{}, len(state.Board.pieces))
	waitingPieceID := ""
	for cell, piece := range state.Board.pieces {
		if _, _, ok := cellPosition(cell); !ok {
			return fmt.Errorf("board contains invalid cell %q", cell)
		}
		if piece.ID == "" {
			return fmt.Errorf("board cell %q has an empty piece id", cell)
		}
		if _, duplicate := seenPieceIDs[piece.ID]; duplicate {
			return fmt.Errorf("board piece id %q is duplicated", piece.ID)
		}
		seenPieceIDs[piece.ID] = struct{}{}
		if !piece.SelectedBy.valid() {
			return fmt.Errorf("board piece %q has invalid selecting team", piece.ID)
		}
		slot, ok := state.PoolSlots[piece.SourcePoolSlotID]
		if !ok || slot.State != PoolSlotSelected || slot.Mod != piece.Mod {
			return fmt.Errorf("board piece %q does not match a selected pool slot", piece.ID)
		}
		if _, duplicate := seenPoolSlotIDs[piece.SourcePoolSlotID]; duplicate {
			return fmt.Errorf("pool slot %q produced multiple board pieces", piece.SourcePoolSlotID)
		}
		seenPoolSlotIDs[piece.SourcePoolSlotID] = struct{}{}
		if piece.Mod == ModFM {
			if piece.ForceMod == nil || (*piece.ForceMod != ForceModNM && *piece.ForceMod != ForceModHD && *piece.ForceMod != ForceModHR) {
				return fmt.Errorf("FM board piece %q requires a valid force mod", piece.ID)
			}
		} else if piece.ForceMod != nil {
			return fmt.Errorf("non-FM board piece %q cannot have a force mod", piece.ID)
		}
		switch piece.Outcome {
		case OutcomeWaitingResult:
			if piece.Owner != nil || waitingPieceID != "" {
				return fmt.Errorf("invalid waiting-result board piece %q", piece.ID)
			}
			waitingPieceID = piece.ID
		case OutcomeWon, OutcomeDead:
			if piece.Owner == nil || !piece.Owner.valid() {
				return fmt.Errorf("board piece %q requires a valid owner", piece.ID)
			}
		case OutcomeWhite:
			if piece.Mod != ModShiro || piece.Owner != nil {
				return fmt.Errorf("board piece %q is not a valid unowned Shiro", piece.ID)
			}
		default:
			return fmt.Errorf("board piece %q has invalid outcome %q", piece.ID, piece.Outcome)
		}
	}
	for id, slot := range state.PoolSlots {
		_, hasPiece := seenPoolSlotIDs[id]
		if (slot.State == PoolSlotSelected) != hasPiece {
			return fmt.Errorf("selected state for pool slot %q does not match the board", id)
		}
	}
	if state.Phase == PhaseWaitingForResult {
		if state.PendingPieceID == "" || state.PendingPieceID != waitingPieceID {
			return fmt.Errorf("waiting-result phase does not identify its pending piece")
		}
	} else if state.PendingPieceID != "" || waitingPieceID != "" {
		return fmt.Errorf("pending piece exists outside waiting-result phase")
	}
	return nil
}

func validateLifecycleState(state State) error {
	if state.Lifecycle != LifecycleReady && state.Version == 0 {
		return fmt.Errorf("non-READY state must have a committed version")
	}
	switch state.Lifecycle {
	case LifecycleReady:
		if state.Version != 0 || state.Phase != PhaseNone || state.ActiveTeam != "" || state.Suspension != nil ||
			state.Winner != nil || state.Result != nil || state.Stalemate != nil || state.AbortReason != "" {
			return fmt.Errorf("READY state contains active or terminal data")
		}
		if len(state.Board.pieces) != 0 {
			return fmt.Errorf("READY state contains placed pieces")
		}
		for id, slot := range state.PoolSlots {
			if slot.State != PoolSlotAvailable {
				return fmt.Errorf("READY state pool slot %q is not available", id)
			}
		}
		if state.RobberyUsed[TeamRed] || state.RobberyUsed[TeamBlue] ||
			state.TeamPauseUsed[TeamRed] || state.TeamPauseUsed[TeamBlue] {
			return fmt.Errorf("READY state contains used team entitlements")
		}
	case LifecycleRunning:
		if state.Phase == PhaseNone || state.Suspension != nil || state.Winner != nil ||
			state.Result != nil || state.Stalemate != nil || state.AbortReason != "" {
			return fmt.Errorf("RUNNING state has inconsistent lifecycle data")
		}
		if err := validateActivePhase(state); err != nil {
			return err
		}
	case LifecycleSuspended:
		if state.Phase == PhaseNone || state.Suspension == nil ||
			state.Winner != nil || state.Result != nil || state.Stalemate != nil || state.AbortReason != "" {
			return fmt.Errorf("SUSPENDED state has inconsistent lifecycle data")
		}
		if state.Suspension.HadTimer && !state.Timer.Paused {
			return fmt.Errorf("SUSPENDED state lost its active timer")
		}
		if !state.Suspension.HadTimer && (state.Timer.Duration > 0 || state.Timer.Paused) {
			return fmt.Errorf("SUSPENDED state fabricated a timer")
		}
		if !state.Suspension.HadTimer && state.Suspension.TimerWasPaused {
			return fmt.Errorf("SUSPENDED state fabricated prior pause evidence")
		}
		if err := validateActivePhase(state); err != nil {
			return err
		}
	case LifecycleAdjudicationRequired:
		if state.Phase != PhaseNone || state.ActiveTeam != "" || state.Stalemate == nil ||
			state.Winner != nil || state.Result != nil || state.Suspension != nil || state.AbortReason != "" {
			return fmt.Errorf("ADJUDICATION_REQUIRED state lacks stalemate evidence or contains terminal data")
		}
	case LifecycleFinished:
		if state.Phase != PhaseNone || state.ActiveTeam != "" || state.Winner == nil || state.Result == nil ||
			!state.Winner.valid() || state.Result.Winner != *state.Winner || state.Suspension != nil ||
			state.Stalemate != nil || state.AbortReason != "" {
			return fmt.Errorf("FINISHED state has inconsistent result data")
		}
	case LifecycleAborted:
		if state.Phase != PhaseNone || state.ActiveTeam != "" || state.AbortReason == "" ||
			state.Winner != nil || state.Result != nil || state.Suspension != nil || state.Stalemate != nil {
			return fmt.Errorf("ABORTED state has inconsistent terminal data")
		}
	}
	return nil
}

func validateActivePhase(state State) error {
	switch state.Phase {
	case PhaseBan, PhasePick, PhaseWaitingForResult:
		if !state.ActiveTeam.valid() {
			return fmt.Errorf("phase %s requires an active team", state.Phase)
		}
	case PhaseTBPreparation, PhaseTBPlaying:
		if state.ActiveTeam != "" {
			return fmt.Errorf("phase %s cannot have an active team", state.Phase)
		}
	default:
		return fmt.Errorf("lifecycle cannot use phase %s", state.Phase)
	}
	return nil
}

func validateRecoveryEvidence(state State) error {
	if state.PendingTBRequest != nil {
		pending := state.PendingTBRequest
		if (state.Lifecycle != LifecycleRunning && state.Lifecycle != LifecycleSuspended) || state.Phase != PhasePick ||
			pending.ID == "" || !pending.RequestedBy.valid() ||
			(pending.Basis != TBBasisTurnThirteen && pending.Basis != TBBasisNoFourWithoutRobbery) {
			return fmt.Errorf("pending TB request is inconsistent with match state")
		}
	}
	if state.Suspension != nil && (strings.TrimSpace(state.Suspension.Reason) == "" || state.Suspension.SuspendedAt.IsZero()) {
		return fmt.Errorf("suspension evidence is incomplete")
	}
	if state.Lifecycle == LifecycleAborted && strings.TrimSpace(state.AbortReason) == "" {
		return fmt.Errorf("abort reason is required")
	}
	if state.Lifecycle == LifecycleAdjudicationRequired {
		if state.Stalemate.RedWonCount < 0 || state.Stalemate.BlueWonCount < 0 ||
			state.Stalemate.RedWonCount != state.Stalemate.BlueWonCount {
			return fmt.Errorf("adjudication evidence requires equal non-negative won counts")
		}
		analysis := Analyze(state)
		if !analysis.Stalemate || analysis.WonCounts[TeamRed] != state.Stalemate.RedWonCount ||
			analysis.WonCounts[TeamBlue] != state.Stalemate.BlueWonCount {
			return fmt.Errorf("adjudication evidence does not match board availability and won counts")
		}
	}
	if state.Lifecycle != LifecycleFinished {
		return nil
	}

	result := state.Result
	switch result.Reason {
	case ResultReasonFourAlignment:
		if result.SurrenderingTeam != nil || len(result.ConfirmingPlayerIDs) != 0 || result.RedWonCount != 0 || result.BlueWonCount != 0 {
			return fmt.Errorf("result reason %s contains incompatible evidence", result.Reason)
		}
		if !state.Board.hasFour(result.Winner) {
			return fmt.Errorf("four-alignment result is not supported by the board")
		}
	case ResultReasonTB:
		if result.SurrenderingTeam != nil || len(result.ConfirmingPlayerIDs) != 0 || result.RedWonCount != 0 || result.BlueWonCount != 0 {
			return fmt.Errorf("result reason %s contains incompatible evidence", result.Reason)
		}
	case ResultReasonSurrender:
		if result.RedWonCount != 0 || result.BlueWonCount != 0 {
			return fmt.Errorf("surrender result contains stalemate evidence")
		}
		if result.SurrenderingTeam == nil || !result.SurrenderingTeam.valid() || result.SurrenderingTeam.opponent() != result.Winner {
			return fmt.Errorf("surrender result has inconsistent teams")
		}
		if _, ok := validateSurrenderEvidence(state.Rosters[*result.SurrenderingTeam], result.ConfirmingPlayerIDs); !ok {
			return fmt.Errorf("surrender result has invalid confirmation evidence")
		}
	case ResultReasonStalemateWonCount:
		if result.SurrenderingTeam != nil || len(result.ConfirmingPlayerIDs) != 0 {
			return fmt.Errorf("stalemate result contains surrender evidence")
		}
		if result.RedWonCount < 0 || result.BlueWonCount < 0 || result.RedWonCount == result.BlueWonCount {
			return fmt.Errorf("stalemate result requires unequal non-negative won counts")
		}
		if (result.RedWonCount > result.BlueWonCount) != (result.Winner == TeamRed) {
			return fmt.Errorf("stalemate result winner does not match won counts")
		}
		analysis := Analyze(state)
		if !analysis.Stalemate || analysis.WonCounts[TeamRed] != result.RedWonCount ||
			analysis.WonCounts[TeamBlue] != result.BlueWonCount {
			return fmt.Errorf("stalemate result does not match board availability and won counts")
		}
	default:
		return fmt.Errorf("invalid result reason %q", result.Reason)
	}
	return nil
}
