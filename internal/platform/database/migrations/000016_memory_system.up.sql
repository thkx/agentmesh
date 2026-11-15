CREATE TABLE agent_memories (
    id TEXT PRIMARY KEY,
    memory_type TEXT NOT NULL CHECK (memory_type IN ('episodic','semantic','procedural')),
    status TEXT NOT NULL CHECK (status IN ('candidate','active','archived','rejected')),
    scope_type TEXT NOT NULL CHECK (scope_type IN ('global','goal','agent')),
    scope_id TEXT NOT NULL DEFAULT '',
    goal_id TEXT NOT NULL,
    source_reflection_id TEXT NOT NULL,
    source_run_id TEXT NOT NULL,
    source_index INTEGER NOT NULL CHECK (source_index >= 0),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    recommendation TEXT NOT NULL DEFAULT '',
    confidence REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence_json TEXT NOT NULL,
    tags_json TEXT NOT NULL,
    promotion_reason TEXT NOT NULL DEFAULT '',
    rejection_reason TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    promoted_at INTEGER NOT NULL DEFAULT 0,
    archived_at INTEGER NOT NULL DEFAULT 0,
    last_accessed_at INTEGER NOT NULL DEFAULT 0,
    access_count INTEGER NOT NULL DEFAULT 0,
    UNIQUE(source_reflection_id, source_index),
    FOREIGN KEY(goal_id) REFERENCES agent_goals(id) ON DELETE RESTRICT,
    FOREIGN KEY(goal_id, source_run_id) REFERENCES goal_reflections(goal_id, run_id) ON DELETE RESTRICT,
    CHECK ((scope_type = 'global' AND scope_id = '') OR (scope_type = 'goal' AND scope_id = goal_id) OR (scope_type = 'agent' AND scope_id <> ''))
);
CREATE INDEX idx_agent_memories_goal_created ON agent_memories(goal_id, created_at DESC);
CREATE INDEX idx_agent_memories_status_scope ON agent_memories(status, scope_type, scope_id, confidence DESC, updated_at DESC);
CREATE INDEX idx_agent_memories_source ON agent_memories(source_reflection_id, source_index);

CREATE TRIGGER agent_memories_no_delete BEFORE DELETE ON agent_memories BEGIN SELECT RAISE(ABORT, 'memories are retained; archive or reject instead'); END;
CREATE TRIGGER agent_memories_identity_immutable BEFORE UPDATE OF id, goal_id, source_reflection_id, source_run_id, source_index, title, content, recommendation, confidence, evidence_json, tags_json, created_at ON agent_memories BEGIN SELECT RAISE(ABORT, 'memory source content is immutable'); END;
CREATE TRIGGER agent_memories_lifecycle_guard BEFORE UPDATE ON agent_memories
WHEN NOT (
    (OLD.status = 'candidate' AND NEW.status IN ('active','rejected')) OR
    (OLD.status = 'active' AND NEW.status = 'archived') OR
    (OLD.status = NEW.status)
)
BEGIN SELECT RAISE(ABORT, 'invalid memory lifecycle transition'); END;

CREATE TRIGGER agent_memories_classification_guard BEFORE UPDATE ON agent_memories
WHEN NOT (OLD.status = 'candidate' AND NEW.status = 'active') AND (NEW.memory_type <> OLD.memory_type OR NEW.scope_type <> OLD.scope_type OR NEW.scope_id <> OLD.scope_id OR NEW.promotion_reason <> OLD.promotion_reason OR NEW.promoted_at <> OLD.promoted_at)
BEGIN SELECT RAISE(ABORT, 'memory classification can only change during candidate promotion'); END;
