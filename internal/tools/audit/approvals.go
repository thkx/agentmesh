package audit

import (
	"context"
	"errors"
	"time"
)

// CreateApproval stores only a hash of the opaque token. Approval consumption
// is an atomic UPDATE, so concurrent replays cannot both succeed.
func (s *SQLite) CreateApproval(ctx context.Context, tokenHash, tool, workspace, argsHash string, expires time.Time) error {
	if tokenHash == "" || tool == "" || workspace == "" || argsHash == "" {
		return errors.New("invalid tool approval")
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM tool_approvals WHERE expires_at < ? OR used_at IS NOT NULL`, time.Now().UTC().Add(-24*time.Hour).UnixNano())
	_, err := s.db.ExecContext(ctx, `INSERT INTO tool_approvals(token_hash,tool_name,workspace,args_hash,expires_at,created_at) VALUES(?,?,?,?,?,?)`, tokenHash, tool, workspace, argsHash, expires.UTC().UnixNano(), time.Now().UTC().UnixNano())
	return err
}

func (s *SQLite) ConsumeApproval(ctx context.Context, tokenHash, tool, workspace, argsHash string, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE tool_approvals SET used_at=? WHERE token_hash=? AND tool_name=? AND workspace=? AND args_hash=? AND used_at IS NULL AND expires_at>?`, now.UTC().UnixNano(), tokenHash, tool, workspace, argsHash, now.UTC().UnixNano())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

var _ interface {
	CreateApproval(context.Context, string, string, string, string, time.Time) error
	ConsumeApproval(context.Context, string, string, string, string, time.Time) (bool, error)
} = (*SQLite)(nil)
