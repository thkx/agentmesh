package provider

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	ErrorCodeUnknown        ErrorCode = "unknown"
	ErrorCodeAuthentication ErrorCode = "authentication"
	ErrorCodePermission     ErrorCode = "permission"
	ErrorCodeRateLimit      ErrorCode = "rate_limit"
	ErrorCodeTimeout        ErrorCode = "timeout"
	ErrorCodeCanceled       ErrorCode = "canceled"
	ErrorCodeInvalidRequest ErrorCode = "invalid_request"
	ErrorCodeModelNotFound  ErrorCode = "model_not_found"
	ErrorCodeUnavailable    ErrorCode = "unavailable"
	ErrorCodeProvider       ErrorCode = "provider_error"
)

type Error struct {
	ProviderID ID

	Code       ErrorCode
	Message    string
	StatusCode int
	Retryable  bool

	Cause error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	if e.Message != "" {
		return fmt.Sprintf(
			"provider %s: %s: %s",
			e.ProviderID,
			e.Code,
			e.Message,
		)
	}

	return fmt.Sprintf(
		"provider %s: %s",
		e.ProviderID,
		e.Code,
	)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Cause
}

func NewError(providerID ID, code ErrorCode, message string, cause error) *Error {
	return &Error{
		ProviderID: providerID,
		Code:       code,
		Message:    message,
		Cause:      cause,
	}
}

func IsErrorCode(err error, code ErrorCode) bool {
	var providerErr *Error

	if !errors.As(err, &providerErr) {
		return false
	}

	return providerErr.Code == code
}
