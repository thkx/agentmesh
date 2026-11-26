package openai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	sdk "github.com/openai/openai-go/v3"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

const (
	maxUpstreamErrorBody = 64 << 10
	maxUpstreamErrorText = 240
)

var bearerCredentialPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)

type upstreamErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Type    string `json:"type"`
	} `json:"error"`
	Message string `json:"message"`
}

func classifyHTTPStatus(status int) (provider.ErrorCode, bool) {
	switch status {
	case 400, 422:
		return provider.ErrorCodeInvalidRequest, false
	case 401:
		return provider.ErrorCodeAuthentication, false
	case 403:
		return provider.ErrorCodePermission, false
	case 404:
		return provider.ErrorCodeModelNotFound, false
	case 408:
		return provider.ErrorCodeTimeout, true
	case 429:
		return provider.ErrorCodeRateLimit, true
	default:
		if status >= 500 {
			return provider.ErrorCodeUnavailable, true
		}
		return provider.ErrorCodeProvider, false
	}
}

// truncateUpstreamText limits text by Unicode rune count.
func truncateUpstreamText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func sanitizeUpstreamText(value, apiKey string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.TrimSpace(value)

	if apiKey != "" {
		value = strings.ReplaceAll(value, apiKey, "[REDACTED]")
	}

	value = bearerCredentialPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")

	return truncateUpstreamText(value, maxUpstreamErrorText)
}

// providerHTTPError creates a normalized provider error from sanitized
// upstream details. Its complete Error() string is bounded to the configured
// maximum, including the provider ID and error code prefix.
func providerHTTPError(
	status int,
	message string,
	upstreamCode string,
	upstreamType string,
	apiKey string,
	cause error,
) *provider.Error {
	code, retryable := classifyHTTPStatus(status)

	message = sanitizeUpstreamText(message, apiKey)
	upstreamCode = sanitizeUpstreamText(upstreamCode, apiKey)
	upstreamType = sanitizeUpstreamText(upstreamType, apiKey)

	summary := "upstream returned an HTTP error"
	if message != "" {
		summary = message
	}
	if upstreamCode != "" {
		summary += " (code: " + upstreamCode + ")"
	}
	if upstreamType != "" {
		summary += " (type: " + upstreamType + ")"
	}
	summary = sanitizeUpstreamText(summary, apiKey)

	prefix := fmt.Sprintf("provider %s: %s: ", providerID, code)
	messageLimit := maxUpstreamErrorText - len([]rune(prefix))
	summary = truncateUpstreamText(summary, messageLimit)

	return &provider.Error{
		ProviderID: providerID,
		Code:       code,
		Message:    summary,
		StatusCode: status,
		Retryable:  retryable,
		Cause:      cause,
	}
}

// normalizeNativeHTTPError extracts a bounded error summary from an
// OpenAI-compatible HTTP response.
func normalizeNativeHTTPError(status int, body []byte, apiKey string) error {
	var envelope upstreamErrorEnvelope
	if len(body) > 0 && len(body) <= maxUpstreamErrorBody {
		_ = json.Unmarshal(body, &envelope)
	}

	message := envelope.Error.Message
	if message == "" {
		message = envelope.Message
	}
	return providerHTTPError(
		status,
		message,
		envelope.Error.Code,
		envelope.Error.Type,
		apiKey,
		nil,
	)
}

func normalizeSDKHTTPError(err *sdk.Error, apiKey string) error {
	if err == nil {
		return nil
	}
	return providerHTTPError(
		err.StatusCode,
		err.Message,
		err.Code,
		err.Type,
		apiKey,
		nil,
	)
}
