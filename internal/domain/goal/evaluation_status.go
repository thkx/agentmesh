package goal

// StatusCompleted is derived from completed_at; the persisted status column remains
// active/canceled to preserve the original SQLite CHECK constraint and migrations.
const StatusCompleted Status = "completed"
