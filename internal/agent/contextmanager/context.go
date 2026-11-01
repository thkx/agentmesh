package contextmanager

import (
	"encoding/json"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"strings"
)

// Trim keeps the first system/developer instructions and the newest messages
// within a deterministic character budget. It never pretends characters are tokens.
// When trimming at a tool-result boundary, it also keeps the matching assistant
// tool-call message (or drops the orphaned tool result if it cannot fit).
func Trim(messages []modelgateway.Message, maxChars int) []modelgateway.Message {
	if maxChars <= 0 || len(messages) == 0 {
		return append([]modelgateway.Message(nil), messages...)
	}
	start := 0
	fixed := make([]modelgateway.Message, 0, 2)
	for start < len(messages) && (messages[start].Role == modelgateway.RoleSystem || messages[start].Role == modelgateway.RoleDeveloper) {
		fixed = append(fixed, messages[start])
		start++
	}
	used := 0
	for _, message := range fixed {
		used += len(message.Content)
	}
	if used >= maxChars {
		return fixed
	}

	selectedStart := len(messages)
	for i := len(messages) - 1; i >= start; i-- {
		n := messageSize(messages[i])
		if used+n > maxChars {
			break
		}
		used += n
		selectedStart = i
	}
	if selectedStart < len(messages) && messages[selectedStart].Role == modelgateway.RoleTool {
		callID := messages[selectedStart].ToolCallID
		match := -1
		for i := selectedStart - 1; i >= start; i-- {
			if messages[i].Role != modelgateway.RoleAssistant {
				continue
			}
			for _, call := range messages[i].ToolCalls {
				if call.ID == callID {
					match = i
					break
				}
			}
			if match >= 0 {
				break
			}
		}
		if match >= 0 {
			extra := 0
			for i := match; i < selectedStart; i++ {
				extra += messageSize(messages[i])
			}
			if used+extra <= maxChars {
				selectedStart = match
			} else {
				selectedStart++
			}
		}
	}
	if selectedStart < len(messages) && messages[selectedStart].Role == modelgateway.RoleUser && strings.HasPrefix(messages[selectedStart].Name, "tool:") && selectedStart > start && messages[selectedStart-1].Role == modelgateway.RoleAssistant {
		previous := messages[selectedStart-1]
		if used+messageSize(previous) <= maxChars {
			selectedStart--
		} else {
			selectedStart++
		}
	}
	out := append([]modelgateway.Message(nil), fixed...)
	if selectedStart < len(messages) {
		out = append(out, messages[selectedStart:]...)
	}
	return out
}

func messageSize(message modelgateway.Message) int {
	size := len(message.Content)
	if len(message.ToolCalls) > 0 {
		if encoded, err := json.Marshal(message.ToolCalls); err == nil {
			size += len(encoded)
		}
	}
	return size
}
