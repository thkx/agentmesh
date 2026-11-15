package collaboration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const maxAgentSessionTranscriptEntryBytes = 256 << 10

type AgentSessionTranscriptEntry struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"session_id"`
	AgentID         string    `json:"agent_id"`
	ExecutionID     string    `json:"execution_id"`
	TaskID          string    `json:"task_id"`
	EntryType       string    `json:"entry_type"`
	Content         string    `json:"content"`
	ContentSHA256   string    `json:"content_sha256"`
	ContentTruncated bool     `json:"content_truncated"`
	CreatedAt       time.Time `json:"created_at"`
}

// AppendAgentSessionTranscript stores one immutable, idempotent execution
// record. Large payloads are represented by a bounded prefix and full digest.
func (s *Service) AppendAgentSessionTranscript(ctx context.Context, entry AgentSessionTranscriptEntry) (AgentSessionTranscriptEntry, error) {
	entry.SessionID = strings.TrimSpace(entry.SessionID)
	entry.AgentID = strings.TrimSpace(entry.AgentID)
	entry.ExecutionID = strings.TrimSpace(entry.ExecutionID)
	entry.TaskID = strings.TrimSpace(entry.TaskID)
	if entry.SessionID == "" || entry.AgentID == "" || entry.ExecutionID == "" || entry.TaskID == "" ||
		(entry.EntryType != "execution_input" && entry.EntryType != "execution_result" && entry.EntryType != "execution_failure") {
		return AgentSessionTranscriptEntry{}, ErrInvalid
	}
	var owner string
	if err := s.db.QueryRowContext(ctx, "SELECT agent_id FROM agent_sessions WHERE id=?", entry.SessionID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentSessionTranscriptEntry{}, ErrNotFound
		}
		return AgentSessionTranscriptEntry{}, err
	}
	if owner != entry.AgentID {
		return AgentSessionTranscriptEntry{}, ErrInvalid
	}
	full := entry.Content
	sum := sha256.Sum256([]byte(full))
	entry.ContentSHA256 = hex.EncodeToString(sum[:])
	if len(entry.Content) > maxAgentSessionTranscriptEntryBytes {
		entry.Content = truncateContextBytes(entry.Content, maxAgentSessionTranscriptEntryBytes)
		entry.ContentTruncated = true
	}
	if entry.ID == "" {
		id, err := newID()
		if err != nil {
			return AgentSessionTranscriptEntry{}, err
		}
		entry.ID = id
	}
	entry.CreatedAt = s.clock().UTC()
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO agent_session_transcript
		(id,session_id,agent_id,execution_id,task_id,entry_type,content,content_sha256,content_truncated,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		entry.ID, entry.SessionID, entry.AgentID, entry.ExecutionID, entry.TaskID, entry.EntryType,
		entry.Content, entry.ContentSHA256, sessionTranscriptBoolInt(entry.ContentTruncated), entry.CreatedAt.UnixMilli())
	if err != nil {
		return AgentSessionTranscriptEntry{}, err
	}
	var stored AgentSessionTranscriptEntry
	var truncated int
	var created int64
	err = s.db.QueryRowContext(ctx, `SELECT id,session_id,agent_id,execution_id,task_id,entry_type,content,content_sha256,content_truncated,created_at
		FROM agent_session_transcript WHERE session_id=? AND execution_id=? AND entry_type=?`,
		entry.SessionID, entry.ExecutionID, entry.EntryType).Scan(
		&stored.ID, &stored.SessionID, &stored.AgentID, &stored.ExecutionID, &stored.TaskID,
		&stored.EntryType, &stored.Content, &stored.ContentSHA256, &truncated, &created)
	if err != nil {
		return AgentSessionTranscriptEntry{}, err
	}
	stored.ContentTruncated = truncated == 1
	stored.CreatedAt = time.UnixMilli(created).UTC()
	if stored.ContentSHA256 != entry.ContentSHA256 || stored.TaskID != entry.TaskID || stored.AgentID != entry.AgentID {
		return AgentSessionTranscriptEntry{}, ErrConflict
	}
	return stored, nil
}

// ListAgentSessionTranscript returns the newest entries in chronological order.
func (s *Service) ListAgentSessionTranscript(ctx context.Context, agentID string, limit int) ([]AgentSessionTranscriptEntry, error) {
	session, err := s.GetActiveAgentSession(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,agent_id,execution_id,task_id,entry_type,content,content_sha256,content_truncated,created_at
		FROM agent_session_transcript WHERE session_id=? ORDER BY created_at DESC,rowid DESC LIMIT ?`, session.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentSessionTranscriptEntry, 0)
	for rows.Next() {
		var item AgentSessionTranscriptEntry
		var truncated int
		var created int64
		if err = rows.Scan(&item.ID, &item.SessionID, &item.AgentID, &item.ExecutionID, &item.TaskID,
			&item.EntryType, &item.Content, &item.ContentSHA256, &truncated, &created); err != nil {
			return nil, err
		}
		item.ContentTruncated = truncated == 1
		item.CreatedAt = time.UnixMilli(created).UTC()
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items, nil
}

// AgentSessionTranscriptContext assembles a bounded historical context snapshot.
func (s *Service) AgentSessionTranscriptContext(ctx context.Context, agentID string, limit, maxChars int) (string, error) {
	if maxChars < 1024 || maxChars > 16000 {
		maxChars = 12000
	}
	items, err := s.ListAgentSessionTranscript(ctx, agentID, limit)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, item := range items {
		header := "\n[Previous execution " + item.EntryType + " task=" + item.TaskID + "]\n"
		if out.Len()+len(header) >= maxChars {
			break
		}
		out.WriteString(header)
		content := item.Content
		if item.ContentTruncated {
			content += "\n[stored content is truncated; sha256=" + item.ContentSHA256 + "]"
		}
		remaining := maxChars - out.Len()
		if len(content) > remaining {
			out.WriteString(truncateContextBytes(content, remaining))
			break
		}
		out.WriteString(content)
		out.WriteByte('\n')
	}
	return out.String(), nil
}

func sessionTranscriptBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
