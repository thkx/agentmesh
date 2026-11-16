package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

func TestOTLPExporterSendsBatchAndAuthHeader(t *testing.T) {
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "***" {
			t.Errorf("authorization header missing")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		} else {
			raw, _ := json.Marshal(body)
			received <- raw
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	provider, err := NewOTLPTraceProvider(server.URL, map[string]string{"Authorization": "***"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, span := provider.Tracer("agentmesh/test").Start(ctx, "test.operation")
	span.SetAttributes(attribute.String("agentmesh.model_id", "model-safe"))
	span.End()
	if err = provider.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-received:
		if !strings.Contains(string(body), "test.operation") || !strings.Contains(string(body), "model-safe") {
			t.Fatalf("span payload missing fields: %s", body)
		}
	case <-ctx.Done():
		t.Fatal("OTLP exporter did not send spans")
	}
	if err = provider.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestOTLPEndpointRequiresTLSOutsideLoopback(t *testing.T) {
	if _, err := NewOTLPTraceProvider("http://collector.example:4318", nil); err == nil {
		t.Fatal("unencrypted remote endpoint accepted")
	}
	if _, err := NewOTLPTraceProvider("https://collector.example:4318", nil); err != nil {
		t.Fatal(err)
	}
}

func TestOTLPHeadersValidation(t *testing.T) {
	headers, err := ParseOTLPHeaders("Authorization=Bearer-token,X-Collector-Key=secret")
	if err != nil {
		t.Fatalf("valid headers rejected: %v", err)
	}
	if headers["Authorization"] != "Bearer-token" || headers["X-Collector-Key"] != "secret" {
		t.Fatalf("unexpected parsed headers: %#v", headers)
	}

	if _, err := NewOTLPTraceProvider("https://collector.example:4318", map[string]string{"Bad Header": "value"}); err == nil {
		t.Fatal("invalid direct exporter header accepted")
	}
	for _, raw := range []string{
		"Authorization=Bearer\r\nInjected: yes",
		"Bad Header=value",
		"=value",
		"Authorization=value\nInjected=yes",
		"authorization=one,Authorization=two",
	} {
		if _, err := ParseOTLPHeaders(raw); err == nil {
			t.Errorf("invalid header accepted: %q", raw)
		}
	}
}
