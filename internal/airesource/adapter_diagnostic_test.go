package airesource

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClassifyTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "cancelled", err: context.Canceled, want: "request_cancelled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "request_timeout"},
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "private.example"}, want: "dns_error"},
		{name: "refused", err: syscall.ECONNREFUSED, want: "connection_refused"},
		{name: "reset", err: syscall.ECONNRESET, want: "connection_reset"},
		{name: "generic", err: errors.New("opaque transport failure"), want: "network_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyTransportError(tt.err); got != tt.want {
				t.Fatalf("classifyTransportError() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDoJSONTimeoutDiagnosticDoesNotExposeURLOrCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	target := server.URL + "/chat/completions?private=query-secret"
	client := &http.Client{Timeout: 10 * time.Millisecond}
	_, status, err := doJSON(context.Background(), client, http.MethodPost, target, "api-key-secret", ProtocolOpenAI, map[string]any{"model": "test"})
	if err == nil {
		t.Fatal("expected request timeout")
	}
	if status != 0 {
		t.Fatalf("status = %d, want 0", status)
	}
	var diagnostic interface{ DiagnosticCode() string }
	if !errors.As(err, &diagnostic) {
		t.Fatalf("error %T does not expose a diagnostic category", err)
	}
	if diagnostic.DiagnosticCode() != "request_timeout" {
		t.Fatalf("diagnostic category = %q, want request_timeout", diagnostic.DiagnosticCode())
	}
	if strings.Contains(err.Error(), "query-secret") || strings.Contains(err.Error(), "api-key-secret") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("diagnostic error leaked sensitive request data: %q", err)
	}
	if got := classifyError(status, err); got != "request_timeout" {
		t.Fatalf("persisted error code = %q, want request_timeout", got)
	}
}

func TestClassifyErrorPreservesHTTPStatusCategories(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{status: http.StatusUnauthorized, want: "authentication_failed"},
		{status: http.StatusForbidden, want: "permission_denied"},
		{status: http.StatusTooManyRequests, want: "rate_limited"},
		{status: http.StatusBadGateway, want: "provider_unavailable"},
	}
	for _, tt := range tests {
		if got := classifyError(tt.status); got != tt.want {
			t.Errorf("classifyError(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}
