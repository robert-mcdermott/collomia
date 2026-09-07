package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// inputError is emitted by native validation before attempting any effect.
// It cannot be established by tool-output text or a model assertion.
type inputError struct{ error }

func (e *inputError) Unwrap() error { return e.error }

func IsInputError(err error) bool {
	var input *inputError
	return errors.As(err, &input)
}

// Missing replacement text is not an intentional empty replacement. Enforce
// that distinction before native file assessment and again before execution.
func decodeFileInput(raw json.RawMessage, target any, required ...string) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &inputError{err}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return &inputError{errors.New("file tool requires exactly one JSON object")}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return &inputError{err}
	}
	for _, field := range required {
		value, ok := fields[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &inputError{fmt.Errorf("%s is required; supply an explicit string (empty text is allowed only when intended); no files changed", field)}
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return &inputError{err}
		}
		if (field == "path" && strings.TrimSpace(text) == "") || (field == "old_text" && text == "") {
			return &inputError{fmt.Errorf("%s must not be empty; no files changed", field)}
		}
	}
	return nil
}
