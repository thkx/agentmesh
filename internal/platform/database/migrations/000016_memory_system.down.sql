DROP TRIGGER IF EXISTS agent_memories_classification_guard;
DROP TRIGGER IF EXISTS agent_memories_lifecycle_guard;
DROP TRIGGER IF EXISTS agent_memories_identity_immutable;
DROP TRIGGER IF EXISTS agent_memories_no_delete;
DROP INDEX IF EXISTS idx_agent_memories_source;
DROP INDEX IF EXISTS idx_agent_memories_status_scope;
DROP INDEX IF EXISTS idx_agent_memories_goal_created;
DROP TABLE IF EXISTS agent_memories;
