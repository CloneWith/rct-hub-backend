package matchengine

import (
	"time"

	"rctHubBackend/internal/domain"
)

// =============================================================================
// Type aliases — all engine types now live in the domain package.
// =============================================================================

// TeamSide identifies one competitive side.
type TeamSide = domain.TeamSide

const (
	TeamRed  = domain.TeamSideRed
	TeamBlue = domain.TeamSideBlue
)

// Lifecycle is the administrative state of a Match aggregate.
type Lifecycle = domain.Lifecycle

const (
	LifecycleReady                = domain.LifecycleReady
	LifecycleRunning              = domain.LifecycleRunning
	LifecycleSuspended            = domain.LifecycleSuspended
	LifecycleAdjudicationRequired = domain.LifecycleAdjudicationRequired
	LifecycleFinished             = domain.LifecycleFinished
	LifecycleAborted              = domain.LifecycleAborted
)

// Phase is the current formal action context.
type Phase = domain.Phase

const (
	PhaseNone             = domain.PhaseNone
	PhaseBan              = domain.PhaseBan
	PhasePick             = domain.PhasePick
	PhaseWaitingForResult = domain.PhaseWaitingForResult
	PhaseTBPreparation    = domain.PhaseTBPreparation
	PhaseTBPlaying        = domain.PhaseTBPlaying
)

// Mod identifies a configured mappool slot category.
type Mod = domain.Mod

const (
	ModNM    = domain.ModNM
	ModHD    = domain.ModHD
	ModHR    = domain.ModHR
	ModDT    = domain.ModDT
	ModFM    = domain.ModFM
	ModShiro = domain.ModShiro
	ModTB    = domain.ModTB
)

// ForceMod is the effective forced mod for an FM board piece.
type ForceMod = domain.ForceMod

const (
	ForceModNM = domain.ForceModNM
	ForceModHD = domain.ForceModHD
	ForceModHR = domain.ForceModHR
)

// PoolSlotState tracks whether a configured slot remains selectable.
type PoolSlotState = domain.PoolSlotState

const (
	PoolSlotAvailable = domain.PoolSlotAvailable
	PoolSlotBanned    = domain.PoolSlotBanned
	PoolSlotSelected  = domain.PoolSlotSelected
)

// PoolSlot is a selectable configured slot. It is not a board piece.
type PoolSlot = domain.PoolSlot

// Outcome describes the competitive state of a concrete board piece.
type Outcome = domain.Outcome

const (
	OutcomeWaitingResult = domain.OutcomeWaitingResult
	OutcomeWon           = domain.OutcomeWon
	OutcomeWhite         = domain.OutcomeWhite
	OutcomeDead          = domain.OutcomeDead
)

// BoardPiece is created from a PoolSlot when a strategist places it.
type BoardPiece = domain.BoardPiece

// Configuration contains the immutable values needed by this engine slice.
type Configuration = domain.Configuration

// Roster is the organizer-approved eight-player team roster.
type Roster = domain.Roster

// Timer is an authoritative server-time window for the current action.
type Timer = domain.Timer

const (
	BanDuration                = 60 * time.Second
	BanAdditionalDuration      = 15 * time.Second
	PickDuration               = 90 * time.Second
	PickAdditionalDuration     = 30 * time.Second
	ResultConfirmationDuration = 20 * time.Second
	TBPreparationDuration      = 90 * time.Second
)

// TimerConfiguration is frozen into the Match aggregate.
type TimerConfiguration = domain.TimerConfiguration

// StandardTimerConfiguration is an alias for the domain default.
var StandardTimerConfiguration = domain.StandardTimerConfiguration

type TBBasis = domain.TBBasis

const (
	TBBasisTurnThirteen         = domain.TBBasisTurnThirteen
	TBBasisNoFourWithoutRobbery = domain.TBBasisNoFourWithoutRobbery
)

type TBRequestState = domain.TBRequestState

type ResultReason = domain.ResultReason

const (
	ResultReasonFourAlignment     = domain.ResultReasonFourAlignment
	ResultReasonTB                = domain.ResultReasonTB
	ResultReasonSurrender         = domain.ResultReasonSurrender
	ResultReasonStalemateWonCount = domain.ResultReasonStalemateWonCount
)

type Result = domain.Result

// StalemateEvidence freezes the decidable evidence.
type StalemateEvidence = domain.StalemateEvidence

// SuspensionState records whether match-level resume may restart the timer.
type SuspensionState = domain.SuspensionState

// State is the pure, serializable Match aggregate used by the rule engine.
type State = domain.State

// Capability is the rule-engine subset of room permissions.
type Capability = domain.Capability

const (
	CapabilityStrategist = domain.CapabilityStrategist
	CapabilityReferee    = domain.CapabilityReferee
)

// Actor contains only authority already established by an outer layer.
type Actor = domain.Actor

// StrategistActor is an alias for the domain constructor.
var StrategistActor = domain.StrategistActor

// RefereeActor is an alias for the domain constructor.
var RefereeActor = domain.RefereeActor

// Command is a closed set of pure domain intents for the implemented slice.
type Command = domain.Command

type StartMatch = domain.StartMatch

type BanPoolSlot = domain.BanPoolSlot

type RefereeBanPoolSlot = domain.RefereeBanPoolSlot

type PlacePiece = domain.PlacePiece

type RefereePlacePiece = domain.RefereePlacePiece

type PlaceShiro = domain.PlaceShiro

type RefereePlaceShiro = domain.RefereePlaceShiro

type RobPiece = domain.RobPiece

type RefereeRobPiece = domain.RefereeRobPiece

type GrantAdditionalTime = domain.GrantAdditionalTime
type CalibrateTimer = domain.CalibrateTimer
type PauseTimer = domain.PauseTimer
type ResumeTimer = domain.ResumeTimer
type SuspendMatch = domain.SuspendMatch
type ResumeMatch = domain.ResumeMatch
type SkipCurrentAction = domain.SkipCurrentAction
type AbortMatch = domain.AbortMatch
type RequestTB = domain.RequestTB
type RefereeRequestTB = domain.RefereeRequestTB
type RespondTBRequest = domain.RespondTBRequest
type RefereeRespondTBRequest = domain.RefereeRespondTBRequest
type StartTB = domain.StartTB
type ConfirmTBResult = domain.ConfirmTBResult
type RecordSurrender = domain.RecordSurrender
type ConfirmBeatmapResult = domain.ConfirmBeatmapResult

type EventType = domain.EventType

const (
	EventMatchStarted                = domain.EventMatchStarted
	EventBanPhaseStarted             = domain.EventBanPhaseStarted
	EventPoolSlotBanned              = domain.EventPoolSlotBanned
	EventTurnAdvanced                = domain.EventTurnAdvanced
	EventPickPhaseStarted            = domain.EventPickPhaseStarted
	EventPiecePlaced                 = domain.EventPiecePlaced
	EventShiroPlaced                 = domain.EventShiroPlaced
	EventResultConfirmationRequested = domain.EventResultConfirmationRequested
	EventBeatmapResultConfirmed      = domain.EventBeatmapResultConfirmed
	EventPieceWon                    = domain.EventPieceWon
	EventPiecesSacrificed            = domain.EventPiecesSacrificed
	EventPieceRobbed                 = domain.EventPieceRobbed
	EventAdditionalTimeGranted       = domain.EventAdditionalTimeGranted
	EventTimerCalibrated             = domain.EventTimerCalibrated
	EventTimerPaused                 = domain.EventTimerPaused
	EventTimerResumed                = domain.EventTimerResumed
	EventMatchSuspended              = domain.EventMatchSuspended
	EventMatchResumed                = domain.EventMatchResumed
	EventActionSkipped               = domain.EventActionSkipped
	EventMatchAborted                = domain.EventMatchAborted
	EventRefereeProxyActionRecorded  = domain.EventRefereeProxyActionRecorded
	EventTBRequested                 = domain.EventTBRequested
	EventTBRequestAccepted           = domain.EventTBRequestAccepted
	EventTBRequestRejected           = domain.EventTBRequestRejected
	EventTBPreparationStarted        = domain.EventTBPreparationStarted
	EventTBStarted                   = domain.EventTBStarted
	EventTBResultConfirmed           = domain.EventTBResultConfirmed
	EventSurrenderRecorded           = domain.EventSurrenderRecorded
	EventMatchFinished               = domain.EventMatchFinished
	EventStalemateDetected           = domain.EventStalemateDetected
	EventAdjudicationRequired        = domain.EventAdjudicationRequired
	EventTimerStarted                = domain.EventTimerStarted
	EventTimerStopped                = domain.EventTimerStopped
)

// Event is a domain fact emitted by an accepted command.
type Event = domain.Event

// Transition is the complete result of one accepted command.
type Transition = domain.Transition

// =============================================================================
// Board types (from domain/engine_board.go)
// =============================================================================

// Cell is a canonical board coordinate from A1 through D4.
type Cell = domain.Cell

// Zone is one of the configured Mod regions on the board.
type Zone = domain.Zone

const (
	ZoneDT = domain.ZoneDT
	ZoneHD = domain.ZoneHD
	ZoneHR = domain.ZoneHR
)

// Board is a fixed 4x4 field. Empty cells are absent from pieces.
type Board = domain.Board

// NewBoard creates an empty 4x4 board.
var NewBoard = domain.NewBoard

// Alignment is a deterministic connected line of WON pieces.
type Alignment = domain.Alignment

// =============================================================================
// Error types (from domain/engine_errors.go)
// =============================================================================

// ErrorCode is a stable machine-readable error classification.
type ErrorCode = domain.ErrorCode

const (
	CodeInvalidRequest            = domain.CodeInvalidRequest
	CodeActionNotAllowed          = domain.CodeActionNotAllowed
	CodeMatchLifecycleConflict    = domain.CodeMatchLifecycleConflict
	CodeMatchPhaseConflict        = domain.CodeMatchPhaseConflict
	CodeNotActiveTeam             = domain.CodeNotActiveTeam
	CodeInvalidPoolSlot           = domain.CodeInvalidPoolSlot
	CodePoolSlotUnavailable       = domain.CodePoolSlotUnavailable
	CodeInvalidBoardCell          = domain.CodeInvalidBoardCell
	CodeInvalidModZone            = domain.CodeInvalidModZone
	CodeResultNotPending          = domain.CodeResultNotPending
	CodeTimerExpired              = domain.CodeTimerExpired
	CodeTimerPaused               = domain.CodeTimerPaused
	CodeTeamPauseAlreadyUsed      = domain.CodeTeamPauseAlreadyUsed
	CodeRobberyNotAvailable       = domain.CodeRobberyNotAvailable
	CodeRobberyRequirementsNotMet = domain.CodeRobberyRequirementsNotMet
	CodeAlignmentOverlap          = domain.CodeAlignmentOverlap
	CodeTBNotAvailable            = domain.CodeTBNotAvailable
	CodeSurrenderEvidenceInvalid  = domain.CodeSurrenderEvidenceInvalid
)

// RuleError is a structured domain error emitted by the engine.
type RuleError = domain.RuleError

// CodeOf unwraps any error chain and returns the innermost ErrorCode.
var CodeOf = domain.CodeOf

// =============================================================================
// Availability types (from domain/engine_availability.go)
// =============================================================================

// PlacementOption is a derived legal placement for the current state.
type PlacementOption = domain.PlacementOption

// Analysis is a read-only snapshot of feasible operations.
type Analysis = domain.Analysis

// Analyze performs a read-only analysis of feasible operations.
var Analyze = domain.Analyze

// =============================================================================
// Engine-only helpers (not in domain)
// =============================================================================

// ruleError constructs a RuleError with the given code and message.
func ruleError(code ErrorCode, message string) error {
	return &RuleError{Code: code, Message: message}
}

// NewReadyState validates the configuration and constructs an immutable-ready
// aggregate. Delegates to domain.NewReadyState.
func NewReadyState(configuration Configuration) (State, error) {
	return domain.NewReadyState(configuration)
}

// cloneRosters is a compatibility shim for engine.go.
func cloneRosters(rosters map[TeamSide]Roster) map[TeamSide]Roster {
	return domain.CloneRosters(rosters)
}

// validateAndCloneRosters is a compatibility shim for engine.go.
func validateAndCloneRosters(rosters map[TeamSide]Roster) (map[TeamSide]Roster, error) {
	return domain.ValidateAndCloneRosters(rosters)
}
