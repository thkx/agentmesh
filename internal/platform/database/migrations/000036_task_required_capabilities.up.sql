ALTER TABLE collaboration_tasks
ADD COLUMN required_capabilities_json TEXT NOT NULL DEFAULT '[]'
CHECK(json_valid(required_capabilities_json));
