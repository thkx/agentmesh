-- Keep the denormalized reflection identifier consistent with its goal/run pair.
CREATE TRIGGER agent_memories_source_reflection_guard
BEFORE INSERT ON agent_memories
WHEN NOT EXISTS (
    SELECT 1
    FROM goal_reflections
    WHERE id = NEW.source_reflection_id
      AND goal_id = NEW.goal_id
      AND run_id = NEW.source_run_id
)
BEGIN
    SELECT RAISE(ABORT, 'memory source reflection does not match goal and run');
END;
