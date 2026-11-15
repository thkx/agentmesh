package protocol

import "testing"

func TestExtractTextJSONAndJSONL(t *testing.T) {
	if got := ExtractText([]byte(`{"result":"done"}`)); got != "done" {
		t.Fatalf("got %q", got)
	}
	raw := []byte("{\"type\":\"message\",\"content\":\"hello\"}\n{\"type\":\"result\",\"result\":\"world\"}")
	if got := ExtractText(raw); got != "hello\nworld" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractSessionIDFromJSONL(t *testing.T) {
	raw := []byte("{\"type\":\"init\",\"session_id\":\"sess-123\"}\n{\"type\":\"result\",\"result\":\"done\"}")
	if got := ExtractSessionID(raw); got != "sess-123" {
		t.Fatalf("got %q", got)
	}
}
