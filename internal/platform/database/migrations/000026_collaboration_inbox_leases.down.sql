DROP INDEX IF EXISTS idx_collaboration_inbox_claim;
-- SQLite cannot drop added columns on all supported versions; migration rollback
-- intentionally preserves the additive lease metadata columns.
