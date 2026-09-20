package citation

import (
	"errors"
	"fmt"
)

var (
	ErrSyntax      = errors.New("invalid YAML")
	ErrType        = errors.New("invalid value type")
	ErrUnsupported = errors.New("unsupported YAML")
	ErrLimit       = errors.New("resource limit exceeded")
	ErrOptions     = errors.New("invalid parse options")
)

// Position identifies a one-based source line and column. Zero means unavailable.
type Position struct {
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

// Diagnostic describes a problem without requiring consumers to parse its message.
type Diagnostic struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
	Position
}

// Error wraps a parse failure and its category for errors.Is and errors.As.
type Error struct {
	Diagnostic
	Cause error
}

func (e *Error) Error() string {
	if e.Line != 0 {
		return fmt.Sprintf("citation: %d:%d: %s", e.Line, e.Column, e.Message)
	}
	return "citation: " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func failure(cause error, code, message string, pos Position) error {
	return &Error{Diagnostic: Diagnostic{Code: code, Message: message, Position: pos}, Cause: cause}
}
