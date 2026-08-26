package errs

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// FieldError describes a single field-level validation failure in a shape the
// frontend can render next to the offending input.
type FieldError struct {
	// Field is the wire-format field path (JSON name, dot-joined for
	// nesting, e.g. "first_pick" or "settings.mp_link").
	Field string `json:"field"`
	// Rule is the failed validation rule, e.g. "required", "oneof", "min".
	Rule string `json:"rule"`
	// Message is a human-readable English explanation of the failure.
	Message string `json:"message"`
}

// ValidationError is an ErrInvalidInput carrying field-level details about
// what was missing or wrong. It unwraps to ErrInvalidInput so existing
// errors.Is(err, errs.ErrInvalidInput) checks keep working.
type ValidationError struct {
	Fields []FieldError
}

// NewValidationError builds a ValidationError from field errors.
func NewValidationError(fields ...FieldError) *ValidationError {
	return &ValidationError{Fields: fields}
}

// AsValidationError extracts a *ValidationError from an error chain.
func AsValidationError(err error) (*ValidationError, bool) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve, true
	}
	return nil, false
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return ErrInvalidInput.Error()
	}
	msgs := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		msgs = append(msgs, f.Message)
	}
	return ErrInvalidInput.Error() + ": " + strings.Join(msgs, "; ")
}

// Unwrap makes errors.Is(err, ErrInvalidInput) return true for validation
// failures, preserving the existing sentinel-based status mapping.
func (e *ValidationError) Unwrap() error { return ErrInvalidInput }

// ValidationErrors converts go-playground validator errors into a
// ValidationError, resolving struct field names to their wire-format
// (JSON) names so clients can map failures back to request fields.
func ValidationErrors(obj any, verrs validator.ValidationErrors) *ValidationError {
	fields := make([]FieldError, 0, len(verrs))
	for _, fe := range verrs {
		name := resolveFieldName(obj, fe)
		fields = append(fields, FieldError{
			Field:   name,
			Rule:    fe.Tag(),
			Message: ruleMessage(name, fe),
		})
	}
	return &ValidationError{Fields: fields}
}

// ruleMessage renders an English explanation for a failed rule.
func ruleMessage(field string, fe validator.FieldError) string {
	param := fe.Param()
	switch fe.Tag() {
	case "required", "required_with", "required_without", "required_if", "required_unless":
		return fmt.Sprintf("%s is required", field)
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, strings.ReplaceAll(param, " ", ", "))
	case "len":
		return fmt.Sprintf("%s must have length %s", field, param)
	case "min":
		return fmt.Sprintf("%s must be at least %s%s", field, param, unitSuffix(fe.Kind()))
	case "max":
		return fmt.Sprintf("%s must be at most %s%s", field, param, unitSuffix(fe.Kind()))
	case "gt":
		return fmt.Sprintf("%s must be greater than %s", field, param)
	case "gte":
		return fmt.Sprintf("%s must be greater than or equal to %s", field, param)
	case "lt":
		return fmt.Sprintf("%s must be less than %s", field, param)
	case "lte":
		return fmt.Sprintf("%s must be less than or equal to %s", field, param)
	case "excludes":
		return fmt.Sprintf("%s must not contain %q", field, param)
	case "url":
		return fmt.Sprintf("%s must be a valid URL", field)
	case "uri":
		return fmt.Sprintf("%s must be a valid URI", field)
	case "email":
		return fmt.Sprintf("%s must be a valid email address", field)
	case "uuid":
		return fmt.Sprintf("%s must be a valid UUID", field)
	case "datetime":
		return fmt.Sprintf("%s must be a datetime in format %s", field, param)
	default:
		if param != "" {
			return fmt.Sprintf("%s failed the %s=%s validation", field, fe.Tag(), param)
		}
		return fmt.Sprintf("%s failed the %s validation", field, fe.Tag())
	}
}

func unitSuffix(kind reflect.Kind) string {
	switch kind {
	case reflect.String:
		return " characters"
	case reflect.Slice, reflect.Array, reflect.Map:
		return " items"
	default:
		return ""
	}
}

// resolveFieldName walks the validator struct namespace from obj and returns
// the wire-format path, e.g. "first_pick" or "mappool.slots[0].mod".
func resolveFieldName(obj any, fe validator.FieldError) string {
	ns := fe.StructNamespace()
	segs := strings.Split(ns, ".")
	t := reflect.TypeOf(obj)
	if t == nil {
		return fe.Field()
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// segs[0] is the root variable name; walk the remaining path.
	if len(segs) < 2 || t.Kind() != reflect.Struct {
		return wireName(t, fe.Field())
	}
	names := make([]string, 0, len(segs)-1)
	for _, seg := range segs[1:] {
		name, indexed := splitIndex(seg)
		if t == nil || t.Kind() != reflect.Struct {
			names = append(names, seg)
			continue
		}
		f, ok := t.FieldByName(name)
		if !ok {
			names = append(names, name)
			t = nil
			continue
		}
		names = append(names, wireName(t, name))
		t = f.Type
		if indexed {
			for {
				switch t.Kind() {
				case reflect.Ptr, reflect.Slice, reflect.Array, reflect.Map:
					t = t.Elem()
				default:
					goto done
				}
			}
		done:
		}
	}
	return strings.Join(names, ".")
}

// splitIndex separates "Field[2]" into ("Field", true). Plain names return
// (seg, false). Map keys are treated as indexed segments too.
func splitIndex(seg string) (string, bool) {
	i := strings.IndexByte(seg, '[')
	if i < 0 {
		return seg, false
	}
	return seg[:i], true
}

// wireName returns the JSON tag name for a struct field, falling back to the
// bson tag (domain types only carry bson tags) and then the Go field name.
func wireName(t reflect.Type, fieldName string) string {
	if t == nil {
		return fieldName
	}
	f, ok := t.FieldByName(fieldName)
	if !ok {
		return fieldName
	}
	if name := firstTagValue(f, "json"); name != "" {
		return name
	}
	if name := firstTagValue(f, "bson"); name != "" {
		return name
	}
	return fieldName
}

func firstTagValue(f reflect.StructField, tag string) string {
	raw := f.Tag.Get(tag)
	if raw == "" || raw == "-" {
		return ""
	}
	name := strings.Split(raw, ",")[0]
	if name == "" {
		return ""
	}
	return name
}
