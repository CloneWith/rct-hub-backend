package errs

import (
	"errors"
	"testing"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

type bindFixture struct {
	Name      string         `json:"name" binding:"required"`
	FirstPick string         `json:"first_pick" binding:"required,oneof=red blue"`
	Players   []int64        `json:"players" binding:"min=4,max=16"`
	Inner     innerFixture   `json:"inner"`
	Mapped    map[string]int `json:"mapped"`
}

type innerFixture struct {
	Value string `json:"value" binding:"required"`
}

func validateFixture(t *testing.T, obj any) validator.ValidationErrors {
	t.Helper()
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		t.Fatal("gin binding engine is not a validator")
	}
	if err := v.Struct(obj); err == nil {
		t.Fatal("expected validation errors, got nil")
	} else {
		var verrs validator.ValidationErrors
		if !errors.As(err, &verrs) {
			t.Fatalf("expected validator.ValidationErrors, got %T", err)
		}
		return verrs
	}
	return nil
}

func TestValidationErrorsResolvesJSONFieldNames(t *testing.T) {
	obj := bindFixture{
		FirstPick: "green",
		Players:   []int64{1, 2},
		Inner:     innerFixture{},
	}
	ve := ValidationErrors(obj, validateFixture(t, obj))

	byField := map[string]FieldError{}
	for _, f := range ve.Fields {
		byField[f.Field] = f
	}

	want := []struct {
		field string
		rule  string
	}{
		{"name", "required"},
		{"first_pick", "oneof"},
		{"players", "min"},
		{"inner.value", "required"},
	}
	for _, w := range want {
		got, ok := byField[w.field]
		if !ok {
			t.Fatalf("missing field %q in %v", w.field, ve.Fields)
		}
		if got.Rule != w.rule {
			t.Fatalf("field %q: rule = %q, want %q", w.field, got.Rule, w.rule)
		}
	}
}

func TestValidationErrorsMessages(t *testing.T) {
	obj := bindFixture{FirstPick: "purple", Players: []int64{1}}
	ve := ValidationErrors(obj, validateFixture(t, obj))

	byField := map[string]string{}
	for _, f := range ve.Fields {
		byField[f.Field] = f.Message
	}

	if got := byField["name"]; got != "name is required" {
		t.Errorf("required message = %q", got)
	}
	if got := byField["first_pick"]; got != "first_pick must be one of: red, blue" {
		t.Errorf("oneof message = %q", got)
	}
	if got := byField["players"]; got != "players must be at least 4 items" {
		t.Errorf("min message = %q", got)
	}
}

func TestValidationErrorIsErrInvalidInput(t *testing.T) {
	ve := NewValidationError(FieldError{Field: "name", Rule: "required", Message: "name is required"})
	if !errors.Is(ve, ErrInvalidInput) {
		t.Fatal("ValidationError must unwrap to ErrInvalidInput")
	}
	if ve.Error() != "invalid input: name is required" {
		t.Fatalf("Error() = %q", ve.Error())
	}

	// A wrapped ValidationError still resolves through the chain.
	wrapped := &AppError{Err: ve, Code: 400}
	if _, ok := AsValidationError(wrapped); !ok {
		t.Fatal("AsValidationError must find wrapped ValidationError")
	}
	if _, ok := AsValidationError(ErrNotFound); ok {
		t.Fatal("AsValidationError must not match unrelated errors")
	}
	if _, ok := AsValidationError(NewValidationError()); !ok {
		t.Fatal("AsValidationError must match empty ValidationError")
	}
	if NewValidationError().Error() != "invalid input" {
		t.Fatalf("empty Error() = %q", NewValidationError().Error())
	}
}
