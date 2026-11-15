CREATE TABLE agent_session_transcript (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
    execution_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    entry_type TEXT NOT NULL CHECK(entry_type IN ('execution_input','execution_result','execution_failure')),
    content TEXT NOT NULL,
    content_sha256 TEXT NOT NULL CHECK(length(content_sha256)=64),
    content_truncated INTEGER NOT NULL DEFAULT 0 CHECK(content_truncated IN (0,1)),
    created_at INTEGER NOT NULL,
    UNIQUE(session_id, execution_id, entry_type)
);

CREATE INDEX idx_agent_session_transcript_recent
    ON agent_session_transcript(session_id, created_at DESC, id DESC);

CREATE TRIGGER agent_session_transcript_no_update
BEFORE UPDATE ON agent_session_transcript
BEGIN
    SELECT RAISE(ABORT, 'agent session transcript is append-only');
END;

CREATE TRIGGER agent_session_transcript_no_delete
BEFORE DELETE ON agent_session_transcript
BEGIN
    SELECT RAISE(ABORT, 'agent session transcript is append-only');
END;
