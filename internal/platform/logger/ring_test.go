package logger

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

type discardHandler struct{}
func(discardHandler) Enabled(context.Context,slog.Level)bool{return true}
func(discardHandler) Handle(context.Context,slog.Record)error{return nil}
func(h discardHandler) WithAttrs([]slog.Attr)slog.Handler{return h}
func(h discardHandler) WithGroup(string)slog.Handler{return h}

func TestRingBufferIsBoundedAndExcludesAttributes(t *testing.T){
	buffer:=NewRingBuffer(2)
	logger:=slog.New(&bufferHandler{next:discardHandler{},buffer:buffer})
	logger.Info("safe startup message","api_key","do-not-retain")
	logger.Warn("second message")
	logger.Error("third message")
	lines:=buffer.Snapshot()
	if len(lines)!=2||!strings.Contains(lines[0],"second message")||!strings.Contains(lines[1],"third message"){t.Fatalf("unexpected ring snapshot: %#v",lines)}
	if strings.Contains(strings.Join(lines," "),"do-not-retain"){t.Fatal("log attributes were retained")}
	buffer.Add(strings.Repeat("x",600))
	if got:=len(buffer.Snapshot()[1]);got>512{t.Fatalf("line length=%d",got)}
	_ = bytes.MinRead
}
