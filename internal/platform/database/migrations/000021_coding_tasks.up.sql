CREATE TABLE coding_tasks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  repository_path TEXT NOT NULL,
  model_id TEXT NOT NULL,
  prompt TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('pending','running','testing','repairing','succeeded','failed','canceled')),
  workspace_id TEXT NOT NULL DEFAULT '',
  workspace_path TEXT NOT NULL DEFAULT '',
  branch_name TEXT NOT NULL DEFAULT '',
  test_command_json TEXT NOT NULL DEFAULT '[]',
  max_repair_attempts INTEGER NOT NULL DEFAULT 2,
  repair_attempts INTEGER NOT NULL DEFAULT 0,
  timeout_seconds INTEGER NOT NULL DEFAULT 1800,
  result_json TEXT NOT NULL DEFAULT '""',
  error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  started_at INTEGER NOT NULL DEFAULT 0,
  finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_coding_tasks_status_updated ON coding_tasks(status, updated_at DESC);
CREATE TABLE coding_task_steps (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  input_json TEXT NOT NULL DEFAULT '{}',
  output_json TEXT NOT NULL DEFAULT '{}',
  error TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE(task_id, sequence),
  FOREIGN KEY(task_id) REFERENCES coding_tasks(id) ON DELETE CASCADE
);
CREATE TABLE coding_task_events (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  FOREIGN KEY(task_id) REFERENCES coding_tasks(id) ON DELETE CASCADE
);
CREATE INDEX idx_coding_task_events_task ON coding_task_events(task_id, created_at);
CREATE TABLE coding_task_snapshots (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  label TEXT NOT NULL,
  commit_hash TEXT NOT NULL DEFAULT '',
  diff TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  FOREIGN KEY(task_id) REFERENCES coding_tasks(id) ON DELETE CASCADE
);
