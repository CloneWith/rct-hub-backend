package matchengine

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	CodeInvalidRequest            ErrorCode = "INVALID_REQUEST"
	CodeActionNotAllowed          ErrorCode = "ACTION_NOT_ALLOWED"
	CodeMatchLifecycleConflict    ErrorCode = "MATCH_LIFECYCLE_CONFLICT"
	CodeMatchPhaseConflict        ErrorCode = "MATCH_PHASE_CONFLICT"
	CodeNotActiveTeam             ErrorCode = "NOT_ACTIVE_TEAM"
	CodeInvalidPoolSlot           ErrorCode = "INVALID_POOL_SLOT"
	CodePoolSlotUnavailable       ErrorCode = "POOL_SLOT_UNAVAILABLE"
	CodeInvalidBoardCell          ErrorCode = "INVALID_BOARD_CELL"
	CodeInvalidModZone            ErrorCode = "INVALID_MOD_ZONE"
	CodeResultNotPending          ErrorCode = "RESULT_NOT_PENDING"
	CodeTimerExpired              ErrorCode = "TIMER_EXPIRED"
	CodeTimerPaused               ErrorCode = "TIMER_PAUSED"
	CodeTeamPauseAlreadyUsed      ErrorCode = "TEAM_PAUSE_ALREADY_USED"
	CodeRobberyNotAvailable       ErrorCode = "ROBBERY_NOT_AVAILABLE"
	CodeRobberyRequirementsNotMet ErrorCode = "ROBBERY_REQUIREMENTS_NOT_MET"
	CodeAlignmentOverlap          ErrorCode = "ALIGNMENT_OVERLAP"
	CodeTBNotAvailable            ErrorCode = "TB_NOT_AVAILABLE"
	CodeSurrenderEvidenceInvalid  ErrorCode = "SURRENDER_EVIDENCE_INVALID"
)

type RuleError struct {
	Code    ErrorCode
	Message string
}

func (e *RuleError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func ruleError(code ErrorCode, message string) error {
	return &RuleError{Code: code, Message: message}
}

func CodeOf(err error) ErrorCode {
	var ruleErr *RuleError
	if errors.As(err, &ruleErr) {
		return ruleErr.Code
	}
	return ""
}
