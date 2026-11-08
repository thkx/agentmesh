package airesource

import (
	"strings"
	"testing"
)

func TestUpstreamErrorDoesNotExposeResponseBody(t *testing.T) {
	err := (&upstreamError{Status: 401, Message: "invalid key secret-value"}).Error()
	if strings.Contains(err, "secret-value") {
		t.Fatalf("upstream body leaked: %s", err)
	}
	if !strings.Contains(err, "401") {
		t.Fatalf("status missing: %s", err)
	}
}
func TestRetryableStatusClassification(t *testing.T) {
	for _, code := range []int{0, 401, 403, 408, 429, 500, 503} {
		if !retryable(&upstreamError{Status: code}) {
			t.Errorf("status %d should be retryable", code)
		}
	}
	for _, code := range []int{400, 404, 422} {
		if retryable(&upstreamError{Status: code}) {
			t.Errorf("status %d should not be retryable", code)
		}
	}
}
func TestCredentialMask(t *testing.T) {
	if got := maskKey("0123456789abcdef"); got != "****cdef" {
		t.Fatalf("maskKey()=%q", got)
	}
	if got := maskKey("short"); got != "****" {
		t.Fatalf("maskKey()=%q", got)
	}
}
