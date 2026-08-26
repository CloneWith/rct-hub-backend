package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"rctHubBackend/pkg/errs"
)

// bindJSON parses the request body into obj, converting binding failures into
// a structured *errs.ValidationError with field-level details so the frontend
// can point at the offending input.
func bindJSON(c *gin.Context, obj any) error {
	if err := c.ShouldBindJSON(obj); err != nil {
		return bindingError(obj, err)
	}
	return nil
}

// bindingError maps gin/validator/encoding failures onto ValidationError.
func bindingError(obj any, err error) error {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		return errs.ValidationErrors(obj, verrs)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		return errs.NewValidationError(errs.FieldError{
			Field:   field,
			Rule:    "type",
			Message: fmt.Sprintf("%s must be of type %s", field, typeErr.Type.String()),
		})
	}
	if errors.Is(err, io.EOF) {
		return errs.NewValidationError(errs.FieldError{
			Field:   "body",
			Rule:    "json",
			Message: "request body is empty",
		})
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return errs.NewValidationError(errs.FieldError{
			Field:   "body",
			Rule:    "json",
			Message: "request body ended unexpectedly",
		})
	}
	return errs.NewValidationError(errs.FieldError{
		Field:   "body",
		Rule:    "json",
		Message: "malformed JSON body",
	})
}
