// Package apikey implements the first-party API-key verifier protocol.
package apikey

import "errors"

// ErrorCode classifies verifier failures without embedding key or token
// material in messages.
type ErrorCode string

const (
	CodeMalformedKeyring ErrorCode = "malformed_keyring"
	CodeUnavailableKey   ErrorCode = "unavailable_key"
	CodeInvalidState     ErrorCode = "invalid_verifier_state"
	CodeVerification     ErrorCode = "verification_failed"
)

// Error is a deliberately non-sensitive verifier error.
type Error struct {
	Code ErrorCode
}

func (e *Error) Error() string {
	switch e.Code {
	case CodeMalformedKeyring:
		return "API verifier keyring is malformed"
	case CodeUnavailableKey:
		return "API verifier key is unavailable"
	case CodeInvalidState:
		return "API verifier state is invalid"
	default:
		return "API credential verification failed"
	}
}

// Is supports errors.Is classification without exposing sensitive details.
func (e *Error) Is(target error) bool {
	var other *Error
	return errors.As(target, &other) && e.Code == other.Code
}

var (
	ErrMalformedKeyring = &Error{Code: CodeMalformedKeyring}
	ErrUnavailableKey   = &Error{Code: CodeUnavailableKey}
	ErrInvalidState     = &Error{Code: CodeInvalidState}
	ErrVerification     = &Error{Code: CodeVerification}
)
