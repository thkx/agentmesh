package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInboxLeaseLost = errors.New("inbox lease lost")

type InboxLease struct {
	InboxEvent
	LeaseOwner     string    `json:"lease_owner"`
	LeaseToken     string    `json:"lease_token"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	Attempts       int       `json:"attempts"`
}

type InboxLeaseOptions struct {
	Owner       string
	Duration    time.Duration
	MaxAttempts int
}

func (s *Service) ClaimInbox(ctx context.Context, agentID string, options InboxLeaseOptions) (InboxLease, error) {
	var out InboxLease
	options.Owner = strings.TrimSpace(options.Owner)
	if options.Owner == "" || options.Duration < time.Second || options.Duration > 24*time.Hour {
		return out, ErrInvalid
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 8
	}
	if options.MaxAttempts > 100 {
		return out, ErrInvalid
	}
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return out, err
	}
	token, err := newID()
	if err != nil {
		return out, err
	}
	now := s.clock().UTC()
	expires := now.Add(options.Duration)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	// Expired work at its retry ceiling becomes terminal instead of being
	// leased forever. This is intentionally scoped to the requested Agent.
	_, err = tx.ExecContext(ctx, `UPDATE collaboration_inbox
		SET status='failed', lease_owner='', lease_token='', lease_expires_at=NULL,
		    handled_at=?, last_error=CASE WHEN last_error='' THEN 'lease expired; retry limit reached' ELSE last_error END
		WHERE agent_id=? AND status='processing' AND lease_expires_at<=? AND attempts>=?`,
		now.UnixMilli(), agentID, now.UnixMilli(), options.MaxAttempts)
	if err != nil {
		return out, err
	}

	var payload string
	var received int64
	var leaseExpiry int64
	err = tx.QueryRowContext(ctx, `UPDATE collaboration_inbox
		SET status='processing', lease_owner=?, lease_token=?, lease_expires_at=?,
		    attempts=attempts+1, last_error=''
		WHERE id=(
			SELECT id FROM collaboration_inbox
			WHERE agent_id=? AND attempts<? AND next_attempt_at<=?
			  AND (status='pending' OR (status='processing' AND lease_expires_at<=?))
			ORDER BY received_at,id LIMIT 1
		)
		RETURNING id,agent_id,event_id,event_type,payload_json,received_at,lease_expires_at,attempts`,
		options.Owner, token, expires.UnixMilli(), agentID, options.MaxAttempts,
		now.UnixMilli(), now.UnixMilli()).
		Scan(&out.ID, &out.AgentID, &out.EventID, &out.EventType, &payload, &received, &leaseExpiry, &out.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		if commitErr := tx.Commit(); commitErr != nil {
			return out, commitErr
		}
		return InboxLease{}, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !json.Valid([]byte(payload)) {
		return out, ErrInvalid
	}
	out.Payload = json.RawMessage(payload)
	out.ReceivedAt = time.UnixMilli(received).UTC()
	out.LeaseOwner = options.Owner
	out.LeaseToken = token
	out.LeaseExpiresAt = time.UnixMilli(leaseExpiry).UTC()
	if err = tx.Commit(); err != nil {
		return InboxLease{}, err
	}
	return out, nil
}

func (s *Service) RenewInboxLease(ctx context.Context, agentID, eventID, owner, token string, duration time.Duration) (time.Time, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(token) == "" || duration < time.Second || duration > 24*time.Hour {
		return time.Time{}, ErrInvalid
	}
	now := s.clock().UTC()
	expires := now.Add(duration)
	res, err := s.db.ExecContext(ctx, `UPDATE collaboration_inbox
		SET lease_expires_at=?
		WHERE agent_id=? AND event_id=? AND status='processing'
		  AND lease_owner=? AND lease_token=? AND lease_expires_at>?`,
		expires.UnixMilli(), agentID, eventID, owner, token, now.UnixMilli())
	if err != nil {
		return time.Time{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	if n != 1 {
		return time.Time{}, ErrInboxLeaseLost
	}
	return expires, nil
}

func (s *Service) AckInboxLease(ctx context.Context, agentID, eventID, owner, token, status string) error {
	if status != "handled" && status != "ignored" {
		return ErrInvalid
	}
	now := s.clock().UTC()
	res, err := s.db.ExecContext(ctx, `UPDATE collaboration_inbox
		SET status=?, handled_at=?, lease_owner='', lease_token='', lease_expires_at=NULL, last_error=''
		WHERE agent_id=? AND event_id=? AND status='processing'
		  AND lease_owner=? AND lease_token=? AND lease_expires_at>?`,
		status, now.UnixMilli(), agentID, eventID, owner, token, now.UnixMilli())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInboxLeaseLost
	}
	return nil
}

// FailInboxLease schedules an at-least-once retry with bounded exponential
// backoff. When maxAttempts is reached, the item becomes terminally failed.
func (s *Service) FailInboxLease(ctx context.Context, agentID, eventID, owner, token, cause string, maxAttempts int) (time.Time, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(token) == "" || maxAttempts < 1 || maxAttempts > 100 {
		return time.Time{}, ErrInvalid
	}
	now := s.clock().UTC()
	var attempts int
	err := s.db.QueryRowContext(ctx, `SELECT attempts FROM collaboration_inbox
		WHERE agent_id=? AND event_id=? AND status='processing'
		  AND lease_owner=? AND lease_token=? AND lease_expires_at>?`,
		agentID, eventID, owner, token, now.UnixMilli()).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrInboxLeaseLost
	}
	if err != nil {
		return time.Time{}, err
	}
	if attempts >= maxAttempts {
		_, err = s.db.ExecContext(ctx, `UPDATE collaboration_inbox
			SET status='failed', handled_at=?, lease_owner='', lease_token='', lease_expires_at=NULL, last_error=?
			WHERE agent_id=? AND event_id=? AND status='processing' AND lease_owner=? AND lease_token=?`,
			now.UnixMilli(), cause, agentID, eventID, owner, token)
		return time.Time{}, err
	}
	delay := time.Second * time.Duration(1<<min(attempts-1, 8))
	next := now.Add(delay)
	res, err := s.db.ExecContext(ctx, `UPDATE collaboration_inbox
		SET status='pending', next_attempt_at=?, lease_owner='', lease_token='', lease_expires_at=NULL, last_error=?
		WHERE agent_id=? AND event_id=? AND status='processing'
		  AND lease_owner=? AND lease_token=? AND lease_expires_at>?`,
		next.UnixMilli(), cause, agentID, eventID, owner, token, now.UnixMilli())
	if err != nil {
		return time.Time{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	if n != 1 {
		return time.Time{}, ErrInboxLeaseLost
	}
	return next, nil
}
