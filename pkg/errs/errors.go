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

func New(err error, message string, code int) *AppError {
	return &AppError{Err: err, Message: message, Code: code}
}
