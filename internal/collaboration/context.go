package collaboration

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/thkx/agentmesh/internal/conversation"
)

// AgentConversationContext returns a bounded snapshot of recent messages from
// channels the Agent explicitly belongs to. It never reads channels merely
// because they share the same Workspace.
func (s *Service) AgentConversationContext(ctx context.Context, agentID string, maxChannels, messagesPerChannel, maxChars int) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", ErrInvalid
	}
	if maxChannels < 1 || maxChannels > 8 {
		maxChannels = 5
	}
	if messagesPerChannel < 1 || messagesPerChannel > 50 {
		messagesPerChannel = 12
	}
	if maxChars < 1024 || maxChars > 16000 {
		maxChars = 12000
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.conversation_id
		FROM collaboration_channels c
		INNER JOIN collaboration_channel_members m ON m.channel_id=c.id
		INNER JOIN agent_identities a ON a.id=m.agent_id AND a.workspace_id=c.workspace_id
		WHERE m.agent_id=? AND c.archived_at IS NULL AND a.status<>'disabled'
		ORDER BY c.created_at DESC, c.id
		LIMIT ?`, agentID, maxChannels)
	if err != nil {
		return "", err
	}
	type channelContext struct{ id, name, conversationID string }
	channels := make([]channelContext, 0, maxChannels)
	for rows.Next() {
		var item channelContext
		if err = rows.Scan(&item.id, &item.name, &item.conversationID); err != nil {
			rows.Close()
			return "", err
		}
		channels = append(channels, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	if err = rows.Close(); err != nil {
		return "", err
	}

	var out strings.Builder
	for _, channel := range channels {
		var latest int64
		if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM conversation_messages WHERE conversation_id=?`, channel.conversationID).Scan(&latest); err != nil {
			return "", err
		}
		after := latest - int64(messagesPerChannel)
		if after < 0 {
			after = 0
		}
		messages, messageErr := s.conversations.Messages(ctx, channel.conversationID, after, messagesPerChannel)
		if messageErr != nil {
			return "", messageErr
		}
		selected := make([]conversation.Message, 0, len(messages))
		for _, message := range messages {
			// System/developer instructions and tool payloads are deliberately
			// excluded. Human and Agent conversation remains untrusted context.
			if message.Role == "user" || message.Role == "assistant" {
				selected = append(selected, message)
			}
		}
		if len(selected) == 0 {
			continue
		}
		header := fmt.Sprintf("\n[Recent channel conversation: %s]\n", truncateContextText(channel.name, 120))
		if out.Len()+len(header) > maxChars {
			break
		}
		out.WriteString(header)
		for _, message := range selected {
			name := strings.TrimSpace(message.Name)
			if name == "" {
				name = message.Role
			}
			line := fmt.Sprintf("%s: %s\n", truncateContextText(name, 80), truncateContextText(message.Content, 1600))
			if out.Len()+len(line) > maxChars {
				remaining := maxChars - out.Len()
				if remaining > 0 {
					out.WriteString(truncateContextBytes(line, remaining))
				}
				return out.String(), nil
			}
			out.WriteString(line)
		}
	}
	return out.String(), nil
}

func truncateContextText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes-1]) + "…"
}

func truncateContextBytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	var out strings.Builder
	for _, r := range value {
		part := string(r)
		if out.Len()+len(part)+len("…") > maxBytes {
			break
		}
		out.WriteString(part)
	}
	if out.Len()+len("…") <= maxBytes {
		out.WriteString("…")
	}
	return out.String()
}
