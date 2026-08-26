package errs

import "errors"

var (
	ErrNotFound      = errors.New("resource not found")
	ErrAlreadyExists = errors.New("resource already exists")
	ErrInvalidInput  = errors.New("invalid input")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrForbidden     = errors.New("forbidden")
	ErrInternal      = errors.New("internal server error")
	ErrCacheSync     = errors.New("cache synchronization failed after write")
	ErrDuplicateKey  = errors.New("duplicate key")
	ErrConflict      = errors.New("resource state conflict")
	// ErrFormalMatchAlreadyStarted tells a caller that another request won the
	// formal-room bootstrap race and the existing match can be returned.
	ErrFormalMatchAlreadyStarted = errors.New("formal room already has a match")
)

// AppError pairs a domain error with an optional public message.
type AppError struct {
	Err     error
	Message string
	Code    int
}

func (e *AppError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Err.Error()
}

// Unwrap exposes the wrapped error so errors.Is / errors.As chains resolve
// through an AppError.
func (e *AppError) Unwrap() error {
	return e.Err
}

func New(err error, message string, code int) *AppError {
	return &AppError{Err: err, Message: message, Code: code}
}
