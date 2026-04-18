-- +goose Up
ALTER TABLE import_history ADD COLUMN status TEXT DEFAULT 'completed' NOT NULL;
ALTER TABLE import_history ADD COLUMN instance_name TEXT DEFAULT NULL;
ALTER TABLE import_history ADD COLUMN metadata TEXT DEFAULT NULL;
CREATE INDEX idx_import_history_status ON import_history(status);
CREATE INDEX idx_import_history_instance_name ON import_history(instance_name);

ALTER TABLE import_queue ADD COLUMN instance_name TEXT DEFAULT NULL;
CREATE INDEX idx_import_queue_instance_name ON import_queue(instance_name);

-- +goose Down
DROP INDEX IF EXISTS idx_import_queue_instance_name;
ALTER TABLE import_queue DROP COLUMN instance_name;

DROP INDEX IF EXISTS idx_import_history_instance_name;
DROP INDEX IF EXISTS idx_import_history_status;
ALTER TABLE import_history DROP COLUMN metadata;
ALTER TABLE import_history DROP COLUMN instance_name;
ALTER TABLE import_history DROP COLUMN status;
