package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type SQLiteStore struct{ db *sql.DB }

func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	if db == nil {
		return nil, errors.New("agent state database is nil")
	}
	return &SQLiteStore{db: db}, nil
}
func (s *SQLiteStore) Save(ctx context.Context, snapshot Snapshot) error {
	if snapshot.Version == 0 {
		snapshot.Version = 1
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	payload, err := Encode(snapshot)
	if err != nil {
		return fmt.Errorf("encode agent state: %w", err)
	}
	now := time.Now().UTC().UnixNano()
	if !snapshot.UpdatedAt.IsZero() {
		now = snapshot.UpdatedAt.UTC().UnixNano()
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_task_states(task_id,state_json,created_at,updated_at) VALUES(?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET state_json=excluded.state_json, updated_at=excluded.updated_at`, snapshot.Task.ID, string(payload), now, now)
	if err != nil {
		return fmt.Errorf("save agent state: %w", err)
	}
	return nil
}
func (s *SQLiteStore) Load(ctx context.Context, id string) (Snapshot, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT state_json FROM agent_task_states WHERE task_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("load agent state: %w", err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode agent state: %w", err)
	}
	if snapshot.Version == 0 {
		snapshot.Version = 1
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("validate persisted agent state: %w", err)
	}
	return snapshot, nil
}
