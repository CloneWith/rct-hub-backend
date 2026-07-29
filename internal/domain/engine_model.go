package domain

import (
	"time"
)

// =============================================================================
// Team Side
// =============================================================================

// TeamSide identifies the red or blue side.
type TeamSide string

const (
	TeamSideRed  TeamSide = "red"
	TeamSideBlue TeamSide = "blue"
)

// Valid reports whether the side is a recognised competitive side.
func (s TeamSide) Valid() bool {
	return s == TeamSideRed || s == TeamSideBlue
}

// Opponent returns the other team side.
func (s TeamSide) Opponent() TeamSide {
	if s == TeamSideRed {
		return TeamSideBlue
	}
	return TeamSideRed
}

// =============================================================================
// Mod & ForceMod
// =============================================================================

// Mod identifies a configured mappool slot category.
type Mod string

const (
	ModNM    Mod = "NM"
	ModHD    Mod = "HD"
	ModHR    Mod = "HR"
	ModDT    Mod = "DT"
	ModFM    Mod = "FM"
	ModShiro Mod = "Shiro"
	ModTB    Mod = "TB"
)

// Valid reports whether the mod value is recognised.
func (m Mod) Valid() bool {
	switch m {
	case ModNM, ModHD, ModHR, ModDT, ModFM, ModShiro, ModTB:
		return true
	default:
		return false
	}
}

// IsRestrictedMod reports whether pieces of the given mod are constrained to a matching zone.
func IsRestrictedMod(mod Mod) bool {
	switch mod {
	case ModHD, ModHR, ModDT:
		return true
	default:
		return false
	}
}

// IsFreeMod reports whether pieces of the given mod can be placed in any zone.
func IsFreeMod(mod Mod) bool {
	return !IsRestrictedMod(mod)
}

// PieceMod is an alias kept for backward compatibility.
// Deprecated: use Mod instead.
type PieceMod = Mod

// ForceMod is the effective forced mod for an FM board piece.
type ForceMod string

const (
	ForceModNM ForceMod = "NM"
	ForceModHD ForceMod = "HD"
	ForceModHR ForceMod = "HR"
)

// =============================================================================
// Lifecycle & Phase
// =============================================================================

// Lifecycle is the administrative state of a Match aggregate.
type Lifecycle string

const (
	LifecycleReady                Lifecycle = "READY"
	LifecycleRunning              Lifecycle = "RUNNING"
	LifecycleSuspended            Lifecycle = "SUSPENDED"
	LifecycleAdjudicationRequired Lifecycle = "ADJUDICATION_REQUIRED"
	LifecycleFinished             Lifecycle = "FINISHED"
	LifecycleAborted              Lifecycle = "ABORTED"
)

// Phase is the current formal action context during a match.
type Phase string

const (
	PhaseNone             Phase = "NONE"
	PhaseBan              Phase = "BAN"
	PhasePick             Phase = "PICK"
	PhaseWaitingForResult Phase = "WAITING_FOR_RESULT"
	PhaseTBPreparation    Phase = "TB_PREPARATION"
	PhaseTBPlaying        Phase = "TB_PLAYING"
)

// =============================================================================
// Pool Slot
// =============================================================================

// PoolSlotState tracks whether a configured slot remains selectable.
type PoolSlotState string

const (
	PoolSlotAvailable PoolSlotState = "AVAILABLE"
	PoolSlotBanned    PoolSlotState = "BANNED"
	PoolSlotSelected  PoolSlotState = "SELECTED"
)

// PoolSlot is a selectable configured slot. It is not a board piece.
type PoolSlot struct {
	ID    string        `json:"id" bson:"id"`
	Mod   Mod           `json:"mod" bson:"mod"`
	State PoolSlotState `json:"state" bson:"state"`
}

// =============================================================================
// Board Piece
// =============================================================================

// Outcome describes the competitive state of a concrete board piece.
type Outcome string

const (
	OutcomeWaitingResult Outcome = "WAITING_RESULT"
	OutcomeWon           Outcome = "WON"
	OutcomeWhite         Outcome = "WHITE"
	OutcomeDead          Outcome = "DEAD"
)

// BoardPiece is created from a PoolSlot when a strategist places it.
type BoardPiece struct {
	ID               string    `json:"id" bson:"id"`
	SourcePoolSlotID string    `json:"sourcePoolSlotId" bson:"source_pool_slot_id"`
	Mod              Mod       `json:"mod" bson:"mod"`
	ForceMod         *ForceMod `json:"forceMod,omitempty" bson:"force_mod,omitempty"`
	SelectedBy       TeamSide  `json:"selectedBy" bson:"selected_by"`
	Owner            *TeamSide `json:"owner,omitempty" bson:"owner,omitempty"`
	Outcome          Outcome   `json:"outcome" bson:"outcome"`
}

// =============================================================================
// Configuration
// =============================================================================

// Configuration contains the immutable values needed by the engine slice.
type Configuration struct {
	FirstBan  TeamSide            `json:"firstBan" bson:"first_ban"`
	FirstPick TeamSide            `json:"firstPick" bson:"first_pick"`
	PoolSlots []PoolSlot          `json:"poolSlots" bson:"pool_slots"`
	Rosters   map[TeamSide]Roster `json:"rosters" bson:"rosters"`
	Timers    TimerConfiguration  `json:"timers" bson:"timers"`
}

// Roster is the organizer-approved team roster.
type Roster struct {
	LeaderID  int64   `json:"leaderId" bson:"leader_id"`
	PlayerIDs []int64 `json:"playerIds" bson:"player_ids"`
}

// =============================================================================
// Timer
// =============================================================================

// Timer is an authoritative server-time window for the current action.
type Timer struct {
	StartedAt        time.Time     `json:"startedAt" bson:"started_at"`
	Duration         time.Duration `json:"duration" bson:"duration"`
	Paused           bool          `json:"paused" bson:"paused"`
	RemainingAtPause time.Duration `json:"remainingAtPause,omitempty" bson:"remaining_at_pause,omitempty"`
}

// Expired reports whether the timer has run out at the given instant.
func (t Timer) Expired(now time.Time) bool {
	return t.Remaining(now) <= 0
}

// Remaining returns the authoritative non-negative duration left at now.
func (t Timer) Remaining(now time.Time) time.Duration {
	if t.Paused {
		if t.RemainingAtPause < 0 {
			return 0
		}
		return t.RemainingAtPause
	}
	remaining := t.Duration - now.Sub(t.StartedAt)
	if remaining < 0 {
		return 0
	}
	if remaining > t.Duration {
		return t.Duration
	}
	return remaining
}

// Pause stops the timer at the given instant.
func (t *Timer) Pause(now time.Time) {
	t.RemainingAtPause = t.Remaining(now)
	t.Paused = true
}

// Resume restarts the timer with the remaining duration at the given instant.
func (t *Timer) Resume(now time.Time) {
	remaining := t.RemainingAtPause
	if remaining < 0 {
		remaining = 0
	}
	*t = Timer{StartedAt: now, Duration: remaining}
}

// Timer duration constants.
const (
	BanDuration                = 60 * time.Second
	BanAdditionalDuration      = 15 * time.Second
	PickDuration               = 90 * time.Second
	PickAdditionalDuration     = 30 * time.Second
	ResultConfirmationDuration = 20 * time.Second
	TBPreparationDuration      = 90 * time.Second
)

// TimerConfiguration is frozen into the Match aggregate.
type TimerConfiguration struct {
	PresetID                     string        `json:"presetId" bson:"preset_id"`
	Ban                          time.Duration `json:"ban" bson:"ban"`
	BanAdditional                time.Duration `json:"banAdditional" bson:"ban_additional"`
	Pick                         time.Duration `json:"pick" bson:"pick"`
	PickAdditional               time.Duration `json:"pickAdditional" bson:"pick_additional"`
	ResultConfirmation           time.Duration `json:"resultConfirmation" bson:"result_confirmation"`
	ResultConfirmationAdditional time.Duration `json:"resultConfirmationAdditional" bson:"result_confirmation_additional"`
	TBPreparation                time.Duration `json:"tbPreparation" bson:"tb_preparation"`
}

// StandardTimerConfiguration returns the RCT S1 default timer preset.
func StandardTimerConfiguration() TimerConfiguration {
	return TimerConfiguration{
		PresetID:                     "RCTS1_STANDARD",
		Ban:                          BanDuration,
		BanAdditional:                BanAdditionalDuration,
		Pick:                         PickDuration,
		PickAdditional:               PickAdditionalDuration,
		ResultConfirmation:           ResultConfirmationDuration,
		ResultConfirmationAdditional: 10 * time.Second,
		TBPreparation:                TBPreparationDuration,
	}
}

// Valid reports whether the timer configuration has all required fields.
func (tc TimerConfiguration) Valid() bool {
	return tc.PresetID != "" &&
		tc.Ban > 0 && tc.BanAdditional > 0 &&
		tc.Pick > 0 && tc.PickAdditional > 0 &&
		tc.ResultConfirmation > 0 && tc.ResultConfirmationAdditional > 0 &&
		tc.TBPreparation > 0
}

// =============================================================================
// TB / Result / Stalemate
// =============================================================================

// TBBasis describes why a tie-breaker was requested.
type TBBasis string

const (
	TBBasisTurnThirteen         TBBasis = "TURN_13"
	TBBasisNoFourWithoutRobbery TBBasis = "NO_FOUR_WITHOUT_ROBBERY"
)

// TBRequestState records a pending tie-breaker negotiation.
type TBRequestState struct {
	ID          string   `json:"id" bson:"id"`
	RequestedBy TeamSide `json:"requestedBy" bson:"requested_by"`
	Basis       TBBasis  `json:"basis" bson:"basis"`
}

// ResultReason explains how the match ended for the rule engine.
type ResultReason string

const (
	ResultReasonFourAlignment     ResultReason = "FOUR_ALIGNMENT"
	ResultReasonTB                ResultReason = "TB"
	ResultReasonSurrender         ResultReason = "SURRENDER"
	ResultReasonStalemateWonCount ResultReason = "STALEMATE_WON_COUNT"
)

// Result stores the final outcome of a match as determined by the rule engine.
type Result struct {
	Winner              TeamSide     `json:"winner" bson:"winner"`
	Reason              ResultReason `json:"reason" bson:"reason"`
	SurrenderingTeam    *TeamSide    `json:"surrenderingTeam,omitempty" bson:"surrendering_team,omitempty"`
	ConfirmingPlayerIDs []int64      `json:"confirmingPlayerIds,omitempty" bson:"confirming_player_ids,omitempty"`
	RedWonCount         int          `json:"redWonCount,omitempty" bson:"red_won_count,omitempty"`
	BlueWonCount        int          `json:"blueWonCount,omitempty" bson:"blue_won_count,omitempty"`
}

// StalemateEvidence freezes the decidable evidence when the deferred
// equal-count scoring rule is required.
type StalemateEvidence struct {
	RedWonCount  int `json:"redWonCount" bson:"red_won_count"`
	BlueWonCount int `json:"blueWonCount" bson:"blue_won_count"`
}

// SuspensionState records whether match-level resume may restart the timer.
type SuspensionState struct {
	Reason         string    `json:"reason" bson:"reason"`
	SuspendedAt    time.Time `json:"suspendedAt" bson:"suspended_at"`
	HadTimer       bool      `json:"hadTimer" bson:"had_timer"`
	TimerWasPaused bool      `json:"timerWasPaused" bson:"timer_was_paused"`
}

// =============================================================================
// Match State (Aggregate Root)
// =============================================================================

// State is the pure, serializable Match aggregate used by the rule engine.
type State struct {
	Version       uint64              `json:"version" bson:"version"`
	Lifecycle     Lifecycle           `json:"lifecycle" bson:"lifecycle"`
	Phase         Phase               `json:"phase" bson:"phase"`
	FirstBan      TeamSide            `json:"firstBan" bson:"first_ban"`
	FirstPick     TeamSide            `json:"firstPick" bson:"first_pick"`
	Turn          int                 `json:"turn" bson:"turn"`
	ActiveTeam    TeamSide            `json:"activeTeam,omitempty" bson:"active_team,omitempty"`
	PoolSlots     map[string]PoolSlot `json:"poolSlots" bson:"pool_slots"`
	Board         Board               `json:"board" bson:"board"`
	Timer         Timer               `json:"timer" bson:"timer"`
	RobberyUsed   map[TeamSide]bool   `json:"robberyUsed" bson:"robbery_used"`
	TeamPauseUsed map[TeamSide]bool   `json:"teamPauseUsed" bson:"team_pause_used"`
	Rosters       map[TeamSide]Roster `json:"rosters" bson:"rosters"`
	Timers        TimerConfiguration  `json:"timers" bson:"timers"`

	PendingPieceID   string             `json:"pendingPieceId,omitempty" bson:"pending_piece_id,omitempty"`
	PendingTBRequest *TBRequestState    `json:"pendingTbRequest,omitempty" bson:"pending_tb_request,omitempty"`
	Winner           *TeamSide          `json:"winner,omitempty" bson:"winner,omitempty"`
	Result           *Result            `json:"result,omitempty" bson:"result,omitempty"`
	Stalemate        *StalemateEvidence `json:"stalemate,omitempty" bson:"stalemate,omitempty"`
	Suspension       *SuspensionState   `json:"suspension,omitempty" bson:"suspension,omitempty"`
	AbortReason      string             `json:"abortReason,omitempty" bson:"abort_reason,omitempty"`
}

// Clone returns an independent state suitable for deterministic transitions.
func (s State) Clone() State {
	clone := s
	clone.PoolSlots = make(map[string]PoolSlot, len(s.PoolSlots))
	for id, slot := range s.PoolSlots {
		clone.PoolSlots[id] = slot
	}
	clone.Board = s.Board.Clone()
	clone.RobberyUsed = make(map[TeamSide]bool, len(s.RobberyUsed))
	for side, used := range s.RobberyUsed {
		clone.RobberyUsed[side] = used
	}
	clone.TeamPauseUsed = make(map[TeamSide]bool, len(s.TeamPauseUsed))
	for side, used := range s.TeamPauseUsed {
		clone.TeamPauseUsed[side] = used
	}
	clone.Rosters = CloneRosters(s.Rosters)
	if s.PendingTBRequest != nil {
		pending := *s.PendingTBRequest
		clone.PendingTBRequest = &pending
	}
	if s.Winner != nil {
		winner := *s.Winner
		clone.Winner = &winner
	}
	if s.Result != nil {
		result := *s.Result
		if s.Result.SurrenderingTeam != nil {
			surrendering := *s.Result.SurrenderingTeam
			result.SurrenderingTeam = &surrendering
		}
		result.ConfirmingPlayerIDs = append([]int64(nil), s.Result.ConfirmingPlayerIDs...)
		clone.Result = &result
	}
	if s.Stalemate != nil {
		stalemate := *s.Stalemate
		clone.Stalemate = &stalemate
	}
	if s.Suspension != nil {
		suspension := *s.Suspension
		clone.Suspension = &suspension
	}
	return clone
}

// NewReadyState validates the configuration and constructs a ready aggregate.
func NewReadyState(configuration Configuration) (State, error) {
	if !configuration.FirstBan.Valid() || !configuration.FirstPick.Valid() {
		return State{}, NewRuleError(CodeInvalidRequest, "first ban and first pick teams must be red or blue")
	}

	pool := make(map[string]PoolSlot, len(configuration.PoolSlots))
	shiroCount := 0
	tbCount := 0
	for _, configured := range configuration.PoolSlots {
		if configured.ID == "" || !configured.Mod.Valid() {
			return State{}, NewRuleError(CodeInvalidRequest, "pool slot id and mod must be valid")
		}
		if _, exists := pool[configured.ID]; exists {
			return State{}, NewRuleError(CodeInvalidRequest, "pool slot ids must be unique")
		}
		configured.State = PoolSlotAvailable
		pool[configured.ID] = configured
		if configured.Mod == ModShiro {
			shiroCount++
		}
		if configured.Mod == ModTB {
			tbCount++
		}
	}
	if shiroCount != 1 || tbCount != 1 {
		return State{}, NewRuleError(CodeInvalidRequest, "configuration requires exactly one Shiro and one TB slot")
	}
	rosters, err := ValidateAndCloneRosters(configuration.Rosters)
	if err != nil {
		return State{}, err
	}
	if !configuration.Timers.Valid() {
		return State{}, NewRuleError(CodeInvalidRequest, "a complete timer preset is required")
	}

	return State{
		Lifecycle: LifecycleReady,
		Phase:     PhaseNone,
		FirstBan:  configuration.FirstBan,
		FirstPick: configuration.FirstPick,
		PoolSlots: pool,
		Board:     NewBoard(),
		RobberyUsed: map[TeamSide]bool{
			TeamSideRed:  false,
			TeamSideBlue: false,
		},
		TeamPauseUsed: map[TeamSide]bool{
			TeamSideRed:  false,
			TeamSideBlue: false,
		},
		Rosters: rosters,
		Timers:  configuration.Timers,
	}, nil
}

// ValidateAndCloneRosters validates roster configuration for both teams.
func ValidateAndCloneRosters(rosters map[TeamSide]Roster) (map[TeamSide]Roster, error) {
	if len(rosters) != 2 {
		return nil, NewRuleError(CodeInvalidRequest, "configuration requires red and blue rosters")
	}
	seen := make(map[int64]struct{}, 16)
	for _, side := range []TeamSide{TeamSideRed, TeamSideBlue} {
		roster, ok := rosters[side]
		if !ok || roster.LeaderID <= 0 || len(roster.PlayerIDs) != 8 {
			return nil, NewRuleError(CodeInvalidRequest, "each team requires eight players and one rostered leader")
		}
		leaderFound := false
		for _, playerID := range roster.PlayerIDs {
			if playerID <= 0 {
				return nil, NewRuleError(CodeInvalidRequest, "roster player ids must be positive")
			}
			if _, duplicate := seen[playerID]; duplicate {
				return nil, NewRuleError(CodeInvalidRequest, "roster player ids must be unique across teams")
			}
			seen[playerID] = struct{}{}
			leaderFound = leaderFound || playerID == roster.LeaderID
		}
		if !leaderFound {
			return nil, NewRuleError(CodeInvalidRequest, "team leader must belong to its roster")
		}
	}
	return CloneRosters(rosters), nil
}

// CloneRosters creates a deep copy of the rosters map.
func CloneRosters(rosters map[TeamSide]Roster) map[TeamSide]Roster {
	clone := make(map[TeamSide]Roster, len(rosters))
	for side, roster := range rosters {
		roster.PlayerIDs = append([]int64(nil), roster.PlayerIDs...)
		clone[side] = roster
	}
	return clone
}

// =============================================================================
// Actor & Capability
// =============================================================================

// Capability is the rule-engine subset of room permissions.
type Capability string

const (
	CapabilityStrategist Capability = "STRATEGIST"
	CapabilityReferee    Capability = "REFEREE"
)

// Actor contains only authority already established by an outer layer.
type Actor struct {
	Capability Capability `json:"capability" bson:"capability"`
	Team       *TeamSide  `json:"team,omitempty" bson:"team,omitempty"`
}

// StrategistActor creates an actor for the given team's strategist.
func StrategistActor(team TeamSide) Actor {
	return Actor{Capability: CapabilityStrategist, Team: &team}
}

// RefereeActor creates an actor with referee authority.
func RefereeActor() Actor {
	return Actor{Capability: CapabilityReferee}
}

// =============================================================================
// Commands (closed set)
// =============================================================================

// Command is a closed set of pure domain intents.
type Command interface {
	isCommand()
}

// StartMatch begins the match from the ready state.
type StartMatch struct{}

func (StartMatch) isCommand() {}

// BanPoolSlot bans a mappool slot.
type BanPoolSlot struct {
	PoolSlotID string
}

func (BanPoolSlot) isCommand() {}

// RefereeBanPoolSlot is a referee-proxied ban.
type RefereeBanPoolSlot struct {
	ActingTeam TeamSide
	PoolSlotID string
	Reason     string
}

func (RefereeBanPoolSlot) isCommand() {}

// PlacePiece places a piece onto the board.
type PlacePiece struct {
	PoolSlotID string
	PieceID    string
	Cell       Cell
}

func (PlacePiece) isCommand() {}

// RefereePlacePiece is a referee-proxied place.
type RefereePlacePiece struct {
	ActingTeam TeamSide
	PoolSlotID string
	PieceID    string
	Cell       Cell
	Reason     string
}

func (RefereePlacePiece) isCommand() {}

// PlaceShiro places the Shiro piece.
type PlaceShiro struct {
	PieceID string
	Cell    Cell
}

func (PlaceShiro) isCommand() {}

// RefereePlaceShiro is a referee-proxied Shiro placement.
type RefereePlaceShiro struct {
	ActingTeam TeamSide
	PieceID    string
	Cell       Cell
	Reason     string
}

func (RefereePlaceShiro) isCommand() {}

// RobPiece executes a robbery.
type RobPiece struct {
	TargetPieceID string
	SacrificeSets [][]string
}

func (RobPiece) isCommand() {}

// RefereeRobPiece is a referee-proxied robbery.
type RefereeRobPiece struct {
	ActingTeam    TeamSide
	TargetPieceID string
	SacrificeSets [][]string
	Reason        string
}

func (RefereeRobPiece) isCommand() {}

// GrantAdditionalTime extends the current timer.
type GrantAdditionalTime struct {
	Reason string
}

func (GrantAdditionalTime) isCommand() {}

// CalibrateTimer adjusts the remaining time.
type CalibrateTimer struct {
	Remaining time.Duration
	Reason    string
}

func (CalibrateTimer) isCommand() {}

// PauseTimer pauses the current timer.
type PauseTimer struct {
	Reason string
}

func (PauseTimer) isCommand() {}

// ResumeTimer resumes a paused timer.
type ResumeTimer struct {
	Reason string
}

func (ResumeTimer) isCommand() {}

// SuspendMatch suspends the match.
type SuspendMatch struct {
	Reason string
}

func (SuspendMatch) isCommand() {}

// ResumeMatch resumes a suspended match.
type ResumeMatch struct {
	Reason string
}

func (ResumeMatch) isCommand() {}

// SkipCurrentAction skips the current turn.
type SkipCurrentAction struct {
	Reason string
}

func (SkipCurrentAction) isCommand() {}

// AbortMatch aborts the match entirely.
type AbortMatch struct {
	Reason string
}

func (AbortMatch) isCommand() {}

// RequestTB requests a tie-breaker.
type RequestTB struct {
	RequestID string
	Basis     TBBasis
}

func (RequestTB) isCommand() {}

// RefereeRequestTB is a referee-proxied TB request.
type RefereeRequestTB struct {
	ActingTeam TeamSide
	RequestID  string
	Basis      TBBasis
	Reason     string
}

func (RefereeRequestTB) isCommand() {}

// RespondTBRequest responds to a TB request.
type RespondTBRequest struct {
	RequestID string
	Accept    bool
}

func (RespondTBRequest) isCommand() {}

// RefereeRespondTBRequest is a referee-proxied TB response.
type RefereeRespondTBRequest struct {
	ActingTeam TeamSide
	RequestID  string
	Accept     bool
	Reason     string
}

func (RefereeRespondTBRequest) isCommand() {}

// StartTB starts the tie-breaker.
type StartTB struct {
	Reason string
}

func (StartTB) isCommand() {}

// ConfirmTBResult confirms the TB outcome.
type ConfirmTBResult struct {
	WinningTeam TeamSide
}

func (ConfirmTBResult) isCommand() {}

// RecordSurrender records a team surrender.
type RecordSurrender struct {
	SurrenderingTeam    TeamSide
	ConfirmingPlayerIDs []int64
	Reason              string
}

func (RecordSurrender) isCommand() {}

// ConfirmBeatmapResult confirms the result of a played beatmap.
type ConfirmBeatmapResult struct {
	BoardPieceID string
	WinningTeam  TeamSide
}

func (ConfirmBeatmapResult) isCommand() {}

// =============================================================================
// Events
// =============================================================================

// EventType categorises a domain event emitted by an accepted command.
type EventType string

const (
	EventMatchStarted                EventType = "MATCH_STARTED"
	EventBanPhaseStarted             EventType = "BAN_PHASE_STARTED"
	EventPoolSlotBanned              EventType = "POOL_SLOT_BANNED"
	EventTurnAdvanced                EventType = "TURN_ADVANCED"
	EventPickPhaseStarted            EventType = "PICK_PHASE_STARTED"
	EventPiecePlaced                 EventType = "PIECE_PLACED"
	EventShiroPlaced                 EventType = "SHIRO_PLACED"
	EventResultConfirmationRequested EventType = "RESULT_CONFIRMATION_REQUESTED"
	EventBeatmapResultConfirmed      EventType = "BEATMAP_RESULT_CONFIRMED"
	EventPieceWon                    EventType = "PIECE_WON"
	EventPiecesSacrificed            EventType = "PIECES_SACRIFICED"
	EventPieceRobbed                 EventType = "PIECE_ROBBED"
	EventAdditionalTimeGranted       EventType = "ADDITIONAL_TIME_GRANTED"
	EventTimerCalibrated             EventType = "TIMER_CALIBRATED"
	EventTimerPaused                 EventType = "TIMER_PAUSED"
	EventTimerResumed                EventType = "TIMER_RESUMED"
	EventMatchSuspended              EventType = "MATCH_SUSPENDED"
	EventMatchResumed                EventType = "MATCH_RESUMED"
	EventActionSkipped               EventType = "ACTION_SKIPPED"
	EventMatchAborted                EventType = "MATCH_ABORTED"
	EventRefereeProxyActionRecorded  EventType = "REFEREE_PROXY_ACTION_RECORDED"
	EventTBRequested                 EventType = "TB_REQUESTED"
	EventTBRequestAccepted           EventType = "TB_REQUEST_ACCEPTED"
	EventTBRequestRejected           EventType = "TB_REQUEST_REJECTED"
	EventTBPreparationStarted        EventType = "TB_PREPARATION_STARTED"
	EventTBStarted                   EventType = "TB_STARTED"
	EventTBResultConfirmed           EventType = "TB_RESULT_CONFIRMED"
	EventSurrenderRecorded           EventType = "SURRENDER_RECORDED"
	EventMatchFinished               EventType = "MATCH_FINISHED"
	EventStalemateDetected           EventType = "STALEMATE_DETECTED"
	EventAdjudicationRequired        EventType = "ADJUDICATION_REQUIRED"
	EventTimerStarted                EventType = "TIMER_STARTED"
	EventTimerStopped                EventType = "TIMER_STOPPED"
)

// Event is a domain fact emitted by an accepted command.
type Event struct {
	Type          EventType     `json:"type" bson:"type"`
	Team          TeamSide      `json:"team,omitempty" bson:"team,omitempty"`
	PoolSlotID    string        `json:"poolSlotId,omitempty" bson:"pool_slot_id,omitempty"`
	BoardPieceID  string        `json:"boardPieceId,omitempty" bson:"board_piece_id,omitempty"`
	BoardPieceIDs []string      `json:"boardPieceIds,omitempty" bson:"board_piece_ids,omitempty"`
	Cell          Cell          `json:"cell,omitempty" bson:"cell,omitempty"`
	Duration      time.Duration `json:"duration,omitempty" bson:"duration,omitempty"`
	Reason        string        `json:"reason,omitempty" bson:"reason,omitempty"`
	RequestID     string        `json:"requestId,omitempty" bson:"request_id,omitempty"`
	PlayerIDs     []int64       `json:"playerIds,omitempty" bson:"player_ids,omitempty"`
}

// =============================================================================
// Transition
// =============================================================================

// Transition is the complete result of one accepted command.
type Transition struct {
	State  State   `json:"state" bson:"state"`
	Events []Event `json:"events" bson:"events"`
}
