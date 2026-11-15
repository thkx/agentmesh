CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    scope_type TEXT NOT NULL DEFAULT 'agent' CHECK(scope_type IN ('global','agent','goal','workflow')),
    scope_id TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    archived_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_conversations_scope_updated ON conversations(scope_type, scope_id, updated_at DESC);

CREATE TABLE conversation_messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('system','developer','user','assistant','tool')),
    name TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    tool_calls_json TEXT NOT NULL DEFAULT '[]',
    tool_call_id TEXT NOT NULL DEFAULT '',
    token_estimate INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    UNIQUE(conversation_id, sequence),
    FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX idx_conversation_messages_recent ON conversation_messages(conversation_id, sequence DESC);

CREATE TABLE conversation_summaries (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    from_sequence INTEGER NOT NULL,
    to_sequence INTEGER NOT NULL,
    summary TEXT NOT NULL,
    token_estimate INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE,
    CHECK(from_sequence > 0 AND to_sequence >= from_sequence)
);
CREATE INDEX idx_conversation_summaries_range ON conversation_summaries(conversation_id, to_sequence DESC);

CREATE TABLE workflow_templates (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    definition_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_workflow_templates_updated ON workflow_templates(updated_at DESC);
