package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

// ExtractText handles common JSON and JSONL event envelopes emitted by coding CLIs.
// Unknown events are ignored; raw output is returned when no recognized text exists.
func ExtractText(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal(trimmed, &obj) == nil {
		if s := findText(obj); s != "" {
			return s
		}
	}
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if s := findText(event); s != "" {
			out = append(out, s)
		}
	}
	if len(out) > 0 {
		return strings.Join(out, "\n")
	}
	return string(trimmed)
}
func findText(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for _, key := range []string{"result", "response", "output", "text", "content", "message"} {
			if value, ok := x[key]; ok {
				if s := findText(value); s != "" {
					return s
				}
			}
		}
		if item, ok := x["item"].(map[string]any); ok {
			if s := findText(item); s != "" {
				return s
			}
		}
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if s := findText(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case string:
		return x
	}
	return ""
}

// ExtractSessionID finds a provider session identifier in common JSON/JSONL envelopes.
func ExtractSessionID(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	if id := sessionIDFromJSON(trimmed); id != "" {
		return id
	}
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	for scanner.Scan() {
		if id := sessionIDFromJSON(scanner.Bytes()); id != "" {
			return id
		}
	}
	return ""
}
func sessionIDFromJSON(raw []byte) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return findSessionID(value)
}
func findSessionID(value any) string {
	switch item := value.(type) {
	case map[string]any:
		for _, key := range []string{"session_id", "sessionId", "sessionID"} {
			if id, ok := item[key].(string); ok && id != "" {
				return id
			}
		}
		for _, child := range item {
			if id := findSessionID(child); id != "" {
				return id
			}
		}
	case []any:
		for _, child := range item {
			if id := findSessionID(child); id != "" {
				return id
			}
		}
	}
	return ""
}
