// Package observability provides an opt-in OTLP/HTTP trace exporter.
// No network telemetry is created unless the operator enables it explicitly.
package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type otlpExporter struct {
	endpoint string
	headers  map[string]string
	client   *http.Client
}

func NewOTLPTraceProvider(endpoint string, headers map[string]string) (*sdktrace.TracerProvider, error) {
	normalized, err := normalizeOTLPEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	copied := make(map[string]string, len(headers))
	for name, value := range headers {
		canonical := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		if canonical == "" || strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("invalid OTLP header")
		}
		copied[canonical] = value
	}
	exporter := &otlpExporter{endpoint: normalized, headers: copied, client: &http.Client{Timeout: 8 * time.Second}}
	res := resource.NewSchemaless(attribute.String("service.name", "agentmesh"))
	return sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(2*time.Second), sdktrace.WithMaxExportBatchSize(128))), nil
}

func ParseOTLPHeaders(raw string) (map[string]string, error) {
	result := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(name) == "" || strings.ContainsAny(value, "\r\n") {
			return nil, errors.New("invalid OTLP headers environment value")
		}
		result[strings.TrimSpace(name)] = value
	}
	return result, nil
}

func normalizeOTLPEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid OTLP endpoint")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("OTLP endpoint must use HTTPS or loopback HTTP")
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("unencrypted OTLP HTTP is allowed only for loopback endpoints")
		}
	}
	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1/traces") {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/traces"
	}
	return u.String(), nil
}

type otlpRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}
type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}
type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes,omitempty"`
}
type otlpScopeSpans struct {
	Scope map[string]string `json:"scope"`
	Spans []otlpSpan        `json:"spans"`
}
type otlpSpan struct {
	TraceID      string          `json:"traceId"`
	SpanID       string          `json:"spanId"`
	ParentSpanID string          `json:"parentSpanId,omitempty"`
	Name         string          `json:"name"`
	Kind         int             `json:"kind"`
	Start        string          `json:"startTimeUnixNano"`
	End          string          `json:"endTimeUnixNano"`
	Attributes   []otlpAttribute `json:"attributes,omitempty"`
	Status       map[string]any  `json:"status,omitempty"`
}
type otlpAttribute struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

func (e *otlpExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	payload := otlpRequest{ResourceSpans: make([]otlpResourceSpans, 0, len(spans))}
	for _, span := range spans {
		attrs := make([]otlpAttribute, 0, len(span.Attributes()))
		for _, item := range span.Attributes() {
			attrs = append(attrs, encodeAttribute(item))
		}
		kind := 1
		switch span.SpanKind() {
		case trace.SpanKindServer:
			kind = 2
		case trace.SpanKindClient:
			kind = 3
		case trace.SpanKindProducer:
			kind = 4
		case trace.SpanKindConsumer:
			kind = 5
		}
		sc := span.SpanContext()
		parent := span.Parent()
		status := map[string]any{"code": 0}
		switch span.Status().Code {
		case codes.Ok:
			status["code"] = 1
		case codes.Error:
			status["code"] = 2
		}
		if span.Status().Code == codes.Error {
			status["message"] = "span operation failed"
		}
		item := otlpSpan{TraceID: sc.TraceID().String(), SpanID: sc.SpanID().String(), Name: span.Name(), Kind: kind, Start: fmt.Sprint(span.StartTime().UnixNano()), End: fmt.Sprint(span.EndTime().UnixNano()), Attributes: attrs, Status: status}
		if parent.IsValid() {
			item.ParentSpanID = parent.SpanID().String()
		}
		resourceAttrs := make([]otlpAttribute, 0)
		if span.Resource() != nil {
			for _, a := range span.Resource().Attributes() {
				resourceAttrs = append(resourceAttrs, encodeAttribute(a))
			}
		}
		scope := span.InstrumentationScope()
		payload.ResourceSpans = append(payload.ResourceSpans, otlpResourceSpans{Resource: otlpResource{Attributes: resourceAttrs}, ScopeSpans: []otlpScopeSpans{{Scope: map[string]string{"name": scope.Name, "version": scope.Version}, Spans: []otlpSpan{item}}}})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("encode OTLP trace payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create OTLP trace request")
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range e.headers {
		req.Header.Set(name, value)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return errors.New("send OTLP trace payload")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OTLP trace endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}
func (e *otlpExporter) Shutdown(context.Context) error { return nil }

func encodeAttribute(item attribute.KeyValue) otlpAttribute {
	value := item.Value
	var encoded map[string]any
	switch value.Type() {
	case attribute.BOOL:
		encoded = map[string]any{"boolValue": value.AsBool()}
	case attribute.INT64:
		encoded = map[string]any{"intValue": fmt.Sprint(value.AsInt64())}
	case attribute.FLOAT64:
		encoded = map[string]any{"doubleValue": value.AsFloat64()}
	case attribute.STRING:
		encoded = map[string]any{"stringValue": value.AsString()}
	default:
		encoded = map[string]any{"stringValue": "[unsupported]"}
	}
	return otlpAttribute{Key: string(item.Key), Value: encoded}
}

// OTLPHeadersFromEnv reads exporter credentials only from the named environment variable.
func OTLPHeadersFromEnv(name string) (map[string]string, error) {
	if strings.TrimSpace(name) == "" {
		return map[string]string{}, nil
	}
	return ParseOTLPHeaders(os.Getenv(name))
}
