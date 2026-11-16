package logger

import (
	"context"
	"log/slog"
	"sync"
)

// RingBuffer retains only bounded static log messages for local diagnostics.
// Attributes are deliberately excluded because they may contain secrets or user data.
type RingBuffer struct {
	mu    sync.RWMutex
	limit int
	lines []string
}

func NewRingBuffer(limit int) *RingBuffer {
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return &RingBuffer{limit: limit}
}

func (b *RingBuffer) Add(line string) {
	if b == nil {
		return
	}
	if len(line) > 512 {
		line = line[:512]
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	if len(b.lines) > b.limit {
		b.lines = append([]string(nil), b.lines[len(b.lines)-b.limit:]...)
	}
}

func (b *RingBuffer) Snapshot() []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]string(nil), b.lines...)
}

type bufferHandler struct {
	next   slog.Handler
	buffer *RingBuffer
}

func (h *bufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *bufferHandler) Handle(ctx context.Context, record slog.Record) error {
	h.buffer.Add(record.Level.String() + ": " + record.Message)
	return h.next.Handle(ctx, record)
}
func (h *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &bufferHandler{next: h.next.WithAttrs(attrs), buffer: h.buffer}
}
func (h *bufferHandler) WithGroup(name string) slog.Handler {
	return &bufferHandler{next: h.next.WithGroup(name), buffer: h.buffer}
}
