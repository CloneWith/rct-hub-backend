package matchcommand

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	CodeInvalidRequest           ErrorCode = "INVALID_REQUEST"
	CodeAuthRequired             ErrorCode = "AUTH_REQUIRED"
	CodeUserNotVerified          ErrorCode = "USER_NOT_VERIFIED"
	CodeUserBanned               ErrorCode = "USER_BANNED"
	CodeGlobalRoleRequired       ErrorCode = "GLOBAL_ROLE_REQUIRED"
	CodeRoomRoleRequired         ErrorCode = "ROOM_ROLE_REQUIRED"
	CodeActionNotAllowed         ErrorCode = "ACTION_NOT_ALLOWED"
	CodeResourceNotFound         ErrorCode = "RESOURCE_NOT_FOUND"
	CodeMatchVersionConflict     ErrorCode = "MATCH_VERSION_CONFLICT"
	CodeDuplicateCommandMismatch ErrorCode = "DUPLICATE_COMMAND_MISMATCH"
	CodeInternalError            ErrorCode = "INTERNAL_ERROR"
)

type Error struct {
	Code           ErrorCode
	Message        string
	CurrentVersion *uint64
	Cause          error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.Cause }

func NewError(code ErrorCode, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func VersionConflict(expected, current uint64) *Error {
	return &Error{
		Code:           CodeMatchVersionConflict,
		Message:        fmt.Sprintf("expected version %d, current version %d", expected, current),
		CurrentVersion: &current,
	}
}

func ErrorOf(err error) *Error {
	var commandErr *Error
	if errors.As(err, &commandErr) {
		return commandErr
	}
	return nil
}
