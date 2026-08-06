package matchengine

import "time"

// TeamSide identifies one competitive side.
type TeamSide string

const (
	TeamRed  TeamSide = "RED"
	TeamBlue TeamSide = "BLUE"
)

func (s TeamSide) valid() bool {
	return s == TeamRed || s == TeamBlue
}

func (s TeamSide) opponent() TeamSide {
	if s == TeamRed {
		return TeamBlue
	}
	return TeamRed
}

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

// Phase is the current formal action context.
type Phase string

const (
	PhaseNone             Phase = "NONE"
	PhaseBan              Phase = "BAN"
	PhasePick             Phase = "PICK"
	PhaseWaitingForResult Phase = "WAITING_FOR_RESULT"
	PhaseTBPreparation    Phase = "TB_PREPARATION"
	PhaseTBPlaying        Phase = "TB_PLAYING"
)

// Mod identifies a configured mappool slot category.
type Mod string

const (
	ModNM    Mod = "NM"
	ModHD    Mod = "HD"
	ModHR    Mod = "HR"
	ModDT    Mod = "DT"
	ModFM    Mod = "FM"
	ModShiro Mod = "SHIRO"
	ModTB    Mod = "TB"
)

func (m Mod) valid() bool {
	switch m {
	case ModNM, ModHD, ModHR, ModDT, ModFM, ModShiro, ModTB:
		return true
	default:
		return false
	}
}

// ForceMod is the effective forced mod for an FM board piece.
type ForceMod string

const (
	ForceModNM ForceMod = "NM"
	ForceModHD ForceMod = "HD"
	ForceModHR ForceMod = "HR"
)

// PoolSlotState tracks whether a configured slot remains selectable.
type PoolSlotState string

const (
	PoolSlotAvailable PoolSlotState = "AVAILABLE"
	PoolSlotBanned    PoolSlotState = "BANNED"
	PoolSlotSelected  PoolSlotState = "SELECTED"
)

// PoolSlot is a selectable configured slot. It is not a board piece.
type PoolSlot struct {
	ID    string        `json:"id"`
	Mod   Mod           `json:"mod"`
	State PoolSlotState `json:"state"`
}

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
	ID               string    `json:"id"`
	SourcePoolSlotID string    `json:"sourcePoolSlotId"`
	Mod              Mod       `json:"mod"`
	ForceMod         *ForceMod `json:"forceMod,omitempty"`
	SelectedBy       TeamSide  `json:"selectedBy"`
	Owner            *TeamSide `json:"owner,omitempty"`
	Outcome          Outcome   `json:"outcome"`
}

// Configuration contains the immutable values needed by this engine slice.
type Configuration struct {
	FirstBan  TeamSide            `json:"firstBan"`
	FirstPick TeamSide            `json:"firstPick"`
	PoolSlots []PoolSlot          `json:"poolSlots"`
	Rosters   map[TeamSide]Roster `json:"rosters"`
	Timers    TimerConfiguration  `json:"timers"`
}

// Roster is the organizer-approved eight-player team roster.
type Roster struct {
	LeaderID  int64   `json:"leaderId"`
	PlayerIDs []int64 `json:"playerIds"`
}

// Timer is an authoritative server-time window for the current action.
type Timer struct {
	StartedAt        time.Time     `json:"startedAt"`
	Duration         time.Duration `json:"duration"`
	Paused           bool          `json:"paused"`
	RemainingAtPause time.Duration `json:"remainingAtPause,omitempty"`
}

func (t Timer) expired(now time.Time) bool {
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

func (t *Timer) pause(now time.Time) {
	t.RemainingAtPause = t.Remaining(now)
	t.Paused = true
}

func (t *Timer) resume(now time.Time) {
	remaining := t.RemainingAtPause
	if remaining < 0 {
		remaining = 0
	}
	*t = Timer{StartedAt: now, Duration: remaining}
}

const (
	BanDuration                = 60 * time.Second
	BanAdditionalDuration      = 15 * time.Second
	PickDuration               = 90 * time.Second
	PickAdditionalDuration     = 30 * time.Second
	ResultConfirmationDuration = 20 * time.Second
	TBPreparationDuration      = 90 * time.Second
)

// TimerConfiguration is frozen into the Match aggregate. The outer
// configuration layer resolves PresetID; the engine requires complete values.
type TimerConfiguration struct {
	PresetID                     string        `json:"presetId"`
	Ban                          time.Duration `json:"ban"`
	BanAdditional                time.Duration `json:"banAdditional"`
	Pick                         time.Duration `json:"pick"`
	PickAdditional               time.Duration `json:"pickAdditional"`
	ResultConfirmation           time.Duration `json:"resultConfirmation"`
	ResultConfirmationAdditional time.Duration `json:"resultConfirmationAdditional"`
	TBPreparation                time.Duration `json:"tbPreparation"`
}

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

func (configuration TimerConfiguration) valid() bool {
	return configuration.PresetID != "" &&
		configuration.Ban > 0 && configuration.BanAdditional > 0 &&
		configuration.Pick > 0 && configuration.PickAdditional > 0 &&
		configuration.ResultConfirmation > 0 && configuration.ResultConfirmationAdditional > 0 &&
		configuration.TBPreparation > 0
}

type TBBasis string

const (
	TBBasisCaptainAgreement         TBBasis = "CAPTAIN_AGREEMENT"
	TBBasisForcedAfterRobberyChecks TBBasis = "FORCED_AFTER_ROBBERY_CHECKS"
)

type TBRequestState struct {
	ID          string   `json:"id"`
	RequestedBy TeamSide `json:"requestedBy"`
	Basis       TBBasis  `json:"basis"`
}

// TBEntryState preserves why the match entered TB across persistence and
// restart. Forced TB has no request or requesting team.
type TBEntryState struct {
	Basis       TBBasis  `json:"basis"`
	RequestID   string   `json:"requestId,omitempty"`
	RequestedBy TeamSide `json:"requestedBy,omitempty"`
}

type ResultReason string

const (
	ResultReasonFourAlignment     ResultReason = "FOUR_ALIGNMENT"
	ResultReasonTB                ResultReason = "TB"
	ResultReasonSurrender         ResultReason = "SURRENDER"
	ResultReasonStalemateWonCount ResultReason = "STALEMATE_WON_COUNT"
)

type Result struct {
	Winner              TeamSide     `json:"winner"`
	Reason              ResultReason `json:"reason"`
	SurrenderingTeam    *TeamSide    `json:"surrenderingTeam,omitempty"`
	ConfirmingPlayerIDs []int64      `json:"confirmingPlayerIds,omitempty"`
	RedWonCount         int          `json:"redWonCount,omitempty"`
	BlueWonCount        int          `json:"blueWonCount,omitempty"`
}

// StalemateEvidence freezes the decidable evidence when the deferred
// equal-count scoring rule is required. It intentionally contains no winner.
type StalemateEvidence struct {
	RedWonCount  int `json:"redWonCount"`
	BlueWonCount int `json:"blueWonCount"`
}

// SuspensionState records whether match-level resume may restart the timer.
// Phase and active team stay visible on State while formal writes are frozen.
type SuspensionState struct {
	Reason         string    `json:"reason"`
	SuspendedAt    time.Time `json:"suspendedAt"`
	HadTimer       bool      `json:"hadTimer"`
	TimerWasPaused bool      `json:"timerWasPaused"`
}

// State is the pure, serializable Match aggregate used by the rule engine.
type State struct {
	Version    uint64              `json:"version"`
	Lifecycle  Lifecycle           `json:"lifecycle"`
	Phase      Phase               `json:"phase"`
	FirstBan   TeamSide            `json:"firstBan"`
	FirstPick  TeamSide            `json:"firstPick"`
	Turn       int                 `json:"turn"`
	ActiveTeam TeamSide            `json:"activeTeam,omitempty"`
	PoolSlots  map[string]PoolSlot `json:"poolSlots"`
	Board      Board               `json:"board"`
	Timer      Timer               `json:"timer"`
	// RobberyUsed records whether a team has robbed at least once. It is TB
	// evidence, not a limit on later robberies.
	RobberyUsed   map[TeamSide]bool   `json:"robberyUsed"`
	TeamPauseUsed map[TeamSide]bool   `json:"teamPauseUsed"`
	Rosters       map[TeamSide]Roster `json:"rosters"`
	Timers        TimerConfiguration  `json:"timers"`

	PendingPieceID   string             `json:"pendingPieceId,omitempty"`
	PendingTBRequest *TBRequestState    `json:"pendingTbRequest,omitempty"`
	TBEntry          *TBEntryState      `json:"tbEntry,omitempty"`
	Winner           *TeamSide          `json:"winner,omitempty"`
	Result           *Result            `json:"result,omitempty"`
	Stalemate        *StalemateEvidence `json:"stalemate,omitempty"`
	Suspension       *SuspensionState   `json:"suspension,omitempty"`
	AbortReason      string             `json:"abortReason,omitempty"`
}

// Turn boundaries are shared by transitions and recovery validation. Ban uses
// -3 through 0; normal placement starts at turn 1.
const (
	readyTurn     = 0
	firstBanTurn  = -3
	secondBanTurn = -2
	thirdBanTurn  = -1
	finalBanTurn  = 0
	firstPickTurn = 1
)

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
	clone.Rosters = cloneRosters(s.Rosters)
	if s.PendingTBRequest != nil {
		pending := *s.PendingTBRequest
		clone.PendingTBRequest = &pending
	}
	if s.TBEntry != nil {
		entry := *s.TBEntry
		clone.TBEntry = &entry
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

// NewReadyState validates the configuration needed by the initial engine
// slice and constructs an immutable-ready aggregate.
func NewReadyState(configuration Configuration) (State, error) {
	if !configuration.FirstBan.valid() || !configuration.FirstPick.valid() {
		return State{}, ruleError(CodeInvalidRequest, "first ban and first pick teams must be RED or BLUE")
	}

	pool := make(map[string]PoolSlot, len(configuration.PoolSlots))
	shiroCount := 0
	tbCount := 0
	for _, configured := range configuration.PoolSlots {
		if configured.ID == "" || !configured.Mod.valid() {
			return State{}, ruleError(CodeInvalidRequest, "pool slot id and mod must be valid")
		}
		if _, exists := pool[configured.ID]; exists {
			return State{}, ruleError(CodeInvalidRequest, "pool slot ids must be unique")
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
		return State{}, ruleError(CodeInvalidRequest, "configuration requires exactly one Shiro and one TB slot")
	}
	rosters, err := validateAndCloneRosters(configuration.Rosters)
	if err != nil {
		return State{}, err
	}
	if !configuration.Timers.valid() {
		return State{}, ruleError(CodeInvalidRequest, "a complete timer preset is required")
	}

	return State{
		Lifecycle: LifecycleReady,
		Phase:     PhaseNone,
		FirstBan:  configuration.FirstBan,
		FirstPick: configuration.FirstPick,
		PoolSlots: pool,
		Board:     NewBoard(),
		RobberyUsed: map[TeamSide]bool{
			TeamRed:  false,
			TeamBlue: false,
		},
		TeamPauseUsed: map[TeamSide]bool{
			TeamRed:  false,
			TeamBlue: false,
		},
		Rosters: rosters,
		Timers:  configuration.Timers,
	}, nil
}

func validateAndCloneRosters(rosters map[TeamSide]Roster) (map[TeamSide]Roster, error) {
	if len(rosters) != 2 {
		return nil, ruleError(CodeInvalidRequest, "configuration requires RED and BLUE rosters")
	}
	seen := make(map[int64]struct{}, 16)
	for _, side := range []TeamSide{TeamRed, TeamBlue} {
		roster, ok := rosters[side]
		if !ok || roster.LeaderID <= 0 || len(roster.PlayerIDs) != 8 {
			return nil, ruleError(CodeInvalidRequest, "each team requires eight players and one rostered leader")
		}
		leaderFound := false
		for _, playerID := range roster.PlayerIDs {
			if playerID <= 0 {
				return nil, ruleError(CodeInvalidRequest, "roster player ids must be positive")
			}
			if _, duplicate := seen[playerID]; duplicate {
				return nil, ruleError(CodeInvalidRequest, "roster player ids must be unique across teams")
			}
			seen[playerID] = struct{}{}
			leaderFound = leaderFound || playerID == roster.LeaderID
		}
		if !leaderFound {
			return nil, ruleError(CodeInvalidRequest, "team leader must belong to its roster")
		}
	}
	return cloneRosters(rosters), nil
}

func cloneRosters(rosters map[TeamSide]Roster) map[TeamSide]Roster {
	clone := make(map[TeamSide]Roster, len(rosters))
	for side, roster := range rosters {
		roster.PlayerIDs = append([]int64(nil), roster.PlayerIDs...)
		clone[side] = roster
	}
	return clone
}

// Capability is the rule-engine subset of room permissions.
type Capability string

const (
	CapabilityStrategist Capability = "STRATEGIST"
	CapabilityCaptain    Capability = "CAPTAIN"
	CapabilityReferee    Capability = "REFEREE"
)

// Actor contains only authority already established by an outer layer.
type Actor struct {
	Capability Capability `json:"capability"`
	Team       *TeamSide  `json:"team,omitempty"`
}

func StrategistActor(team TeamSide) Actor {
	return Actor{Capability: CapabilityStrategist, Team: &team}
}

func CaptainActor(team TeamSide) Actor {
	return Actor{Capability: CapabilityCaptain, Team: &team}
}

func RefereeActor() Actor {
	return Actor{Capability: CapabilityReferee}
}

// Command is a closed set of pure domain intents for the implemented slice.
type Command interface {
	isCommand()
}

type StartMatch struct{}

func (StartMatch) isCommand() {}

type BanPoolSlot struct {
	PoolSlotID string
}

func (BanPoolSlot) isCommand() {}

type RefereeBanPoolSlot struct {
	ActingTeam TeamSide
	PoolSlotID string
	Reason     string
}

func (RefereeBanPoolSlot) isCommand() {}

type PlacePiece struct {
	PoolSlotID string
	PieceID    string
	Cell       Cell
}

func (PlacePiece) isCommand() {}

type RefereePlacePiece struct {
	ActingTeam TeamSide
	PoolSlotID string
	PieceID    string
	Cell       Cell
	Reason     string
}

func (RefereePlacePiece) isCommand() {}

type PlaceShiro struct {
	PieceID string
	Cell    Cell
}

func (PlaceShiro) isCommand() {}

type RefereePlaceShiro struct {
	ActingTeam TeamSide
	PieceID    string
	Cell       Cell
	Reason     string
}

func (RefereePlaceShiro) isCommand() {}

type RobPiece struct {
	TargetPieceID string
	SacrificeSets [][]string
}

func (RobPiece) isCommand() {}

type RefereeRobPiece struct {
	ActingTeam    TeamSide
	TargetPieceID string
	SacrificeSets [][]string
	Reason        string
}

func (RefereeRobPiece) isCommand() {}

type GrantAdditionalTime struct {
	Reason string
}

func (GrantAdditionalTime) isCommand() {}

type CalibrateTimer struct {
	Remaining time.Duration
	Reason    string
}

func (CalibrateTimer) isCommand() {}

type PauseTimer struct {
	Reason string
}

func (PauseTimer) isCommand() {}

type ResumeTimer struct {
	Reason string
}

func (ResumeTimer) isCommand() {}

type SuspendMatch struct {
	Reason string
}

func (SuspendMatch) isCommand() {}

type ResumeMatch struct {
	Reason string
}

func (ResumeMatch) isCommand() {}

type SkipCurrentAction struct {
	Reason string
}

func (SkipCurrentAction) isCommand() {}

type AbortMatch struct {
	Reason string
}

func (AbortMatch) isCommand() {}

type RequestTB struct {
	RequestID string
	Basis     TBBasis
}

func (RequestTB) isCommand() {}

type RefereeRequestTB struct {
	ActingTeam TeamSide
	RequestID  string
	Basis      TBBasis
	Reason     string
}

func (RefereeRequestTB) isCommand() {}

type RespondTBRequest struct {
	RequestID string
	Accept    bool
}

func (RespondTBRequest) isCommand() {}

type RefereeRespondTBRequest struct {
	ActingTeam TeamSide
	RequestID  string
	Accept     bool
	Reason     string
}

func (RefereeRespondTBRequest) isCommand() {}

type StartTB struct {
	Reason string
}

func (StartTB) isCommand() {}

type ConfirmTBResult struct {
	WinningTeam TeamSide
}

func (ConfirmTBResult) isCommand() {}

type RecordSurrender struct {
	SurrenderingTeam    TeamSide
	ConfirmingPlayerIDs []int64
	Reason              string
}

func (RecordSurrender) isCommand() {}

type ConfirmBeatmapResult struct {
	BoardPieceID string
	WinningTeam  TeamSide
}

func (ConfirmBeatmapResult) isCommand() {}

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
	EventTBRequestExpired            EventType = "TB_REQUEST_EXPIRED"
	EventTBForced                    EventType = "TB_FORCED"
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
	Type          EventType     `json:"type"`
	Team          TeamSide      `json:"team,omitempty"`
	PoolSlotID    string        `json:"poolSlotId,omitempty"`
	BoardPieceID  string        `json:"boardPieceId,omitempty"`
	BoardPieceIDs []string      `json:"boardPieceIds,omitempty"`
	Cell          Cell          `json:"cell,omitempty"`
	Duration      time.Duration `json:"duration,omitempty"`
	Reason        string        `json:"reason,omitempty"`
	RequestID     string        `json:"requestId,omitempty"`
	Basis         TBBasis       `json:"tbBasis,omitempty"`
	PlayerIDs     []int64       `json:"playerIds,omitempty"`
}

// Transition is the complete result of one accepted command.
type Transition struct {
	State  State   `json:"state"`
	Events []Event `json:"events"`
}
