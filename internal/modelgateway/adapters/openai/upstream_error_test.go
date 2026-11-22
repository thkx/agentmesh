package openai

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/openai/openai-go/v3"

	"github.com/thkx/agentmesh/internal/domain/provider"
)

func TestClassifyHTTPStatus(t *testing.T) {
	tests := []struct {
		status    int
		wantCode  provider.ErrorCode
		retryable bool
	}{
		{400, provider.ErrorCodeInvalidRequest, false},
		{422, provider.ErrorCodeInvalidRequest, false},
		{401, provider.ErrorCodeAuthentication, false},
		{403, provider.ErrorCodePermission, false},
		{404, provider.ErrorCodeModelNotFound, false},
		{408, provider.ErrorCodeTimeout, true},
		{429, provider.ErrorCodeRateLimit, true},
		{500, provider.ErrorCodeUnavailable, true},
		{502, provider.ErrorCodeUnavailable, true},
		{503, provider.ErrorCodeUnavailable, true},
		{302, provider.ErrorCodeProvider, false},
	}
	for _, tc := range tests {
		t.Run(strings.Join([]string{"status", string(rune(tc.status))}, "-"), func(t *testing.T) {
			code, retryable := classifyHTTPStatus(tc.status)
			if code != tc.wantCode || retryable != tc.retryable {
				t.Fatalf("classifyHTTPStatus(%d) = (%q, %t), want (%q, %t)", tc.status, code, retryable, tc.wantCode, tc.retryable)
			}
		})
	}
}

func TestNormalizeNativeHTTPError(t *testing.T) {
	err := normalizeNativeHTTPError(400, []byte(`{"error":{"message":"invalid parameter","code":"bad_request","type":"invalid_request_error"}}`), "")
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected provider error, got %T", err)
	}
	if providerErr.StatusCode != 400 || providerErr.Code != provider.ErrorCodeInvalidRequest {
		t.Fatalf("unexpected normalized error: %+v", providerErr)
	}
	for _, want := range []string{"invalid parameter", "bad_request", "invalid_request_error"} {
		if !strings.Contains(providerErr.Message, want) {
			t.Errorf("message %q does not contain %q", providerErr.Message, want)
		}
	}
}

func TestNormalizeNativeHTTPErrorRedactsCredentials(t *testing.T) {
	secret := "sk-test-secret-value"
	body := []byte(`{"error":{"message":"request failed with ` + secret + ` and Bearer abc.def"}}`)
	err := normalizeNativeHTTPError(401, body, secret)
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "abc.def") {
		t.Fatalf("credentials leaked in error: %s", err)
	}
}

func TestNormalizeNativeHTTPErrorHandlesInvalidJSON(t *testing.T) {
	err := normalizeNativeHTTPError(502, []byte("not-json"), "")
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected provider error, got %T", err)
	}
	if providerErr.Message != "upstream returned an HTTP error" {
		t.Fatalf("unexpected fallback message: %q", providerErr.Message)
	}
}

func TestSanitizeUpstreamTextLimitsLength(t *testing.T) {
	got := sanitizeUpstreamText(strings.Repeat("界", maxUpstreamErrorText+20), "")
	if length := len([]rune(got)); length != maxUpstreamErrorText {
		t.Fatalf("sanitized text length = %d, want %d", length, maxUpstreamErrorText)
	}
}

func TestSanitizeUpstreamTextRemovesControlCharacters(t *testing.T) {
	got := sanitizeUpstreamText("invalid\nrequest\twith control", "")
	if got != "invalid request with control" {
		t.Fatalf("unexpected sanitized text: %q", got)
	}
}

func TestNormalizeSDKHTTPErrorRedactsAPIKey(t *testing.T) {
	secret := "sk-test-secret-value"
	adapter := &Adapter{apiKey: secret}
	upstreamErr := &sdk.Error{
		StatusCode: 400,
		Message:    "invalid request using credential " + secret,
		Code:       "invalid_request",
		Type:       "invalid_request_error",
	}
	err := adapter.normalizeOpenAIError(upstreamErr)
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("API key leaked in error: %s", err)
	}
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected provider error, got %T", err)
	}
	if providerErr.Cause != nil {
		t.Fatalf("SDK error cause must not be retained: %v", providerErr.Cause)
	}
}

func TestProviderHTTPErrorLimitsFinalMessageLength(t *testing.T) {
	err := providerHTTPError(
		400,
		strings.Repeat("a", 500),
		strings.Repeat("b", 100),
		strings.Repeat("c", 100),
		"",
		nil,
	)
	if got := len([]rune(err.Error())); got > maxUpstreamErrorText {
		t.Fatalf("final error message exceeds limit: got=%d max=%d", got, maxUpstreamErrorText)
	}
}
