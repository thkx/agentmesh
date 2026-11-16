package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type requestContextKey string

const (
	requestIDKey requestContextKey = "agentmesh_request_id"
	traceIDKey   requestContextKey = "agentmesh_trace_id"
)

func RequestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func TraceIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(traceIDKey).(string)
	return value
}

func requestCorrelationMiddleware(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if !validCorrelationID(requestID, 64) {
			requestID = randomHex(16)
		}
		ctx := c.Request.Context()
		var traceID string
		if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
			traceID = spanContext.TraceID().String()
			ctx = context.WithValue(ctx, traceIDKey, traceID)
			trace.SpanFromContext(ctx).SetAttributes(attribute.String("agentmesh.request_id", requestID))
		} else {
			traceID = traceIDFromParent(c.GetHeader("traceparent"))
			if traceID == "" {
				traceID = randomHex(16)
			}
			ctx = context.WithValue(ctx, traceIDKey, traceID)
		}

		ctx = context.WithValue(ctx, requestIDKey, requestID)
		ctx = context.WithValue(ctx, traceIDKey, traceID)
		ctx = modelgateway.WithRequestID(ctx, requestID)
		c.Request = c.Request.WithContext(ctx)
		c.Header("X-Request-ID", requestID)
		c.Header("X-Trace-ID", TraceIDFromContext(ctx))

		started := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		logger.InfoContext(c.Request.Context(), "http request completed",
			"request_id", requestID,
			"trace_id", traceID,
			"method", c.Request.Method,
			"route", route,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}
}

func requestMetricsMiddleware(metrics *observability.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		if metrics != nil {
			metrics.BeginRequest()
			defer metrics.EndRequest()
		}
		started := time.Now()
		c.Next()
		if metrics != nil {
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			metrics.ObserveHTTPRequest(c.Request.Method, route, c.Writer.Status(), time.Since(started))
		}
	}
}

func validCorrelationID(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func traceIDFromParent(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 ||
		!validHex(parts[1]) || !validHex(parts[2]) || !validHex(parts[3]) ||
		strings.EqualFold(parts[1], "00000000000000000000000000000000") ||
		strings.EqualFold(parts[2], "0000000000000000") {
		return ""
	}
	return strings.ToLower(parts[1])
}

func validHex(value string) bool {
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func randomHex(size int) string {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failure is exceptionally rare. Keep a syntactically valid
		// non-secret fallback; this value is only a correlation identifier.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(raw)
}
