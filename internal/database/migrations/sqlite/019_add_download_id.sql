-- +goose Up
ALTER TABLE import_queue ADD COLUMN download_id TEXT DEFAULT NULL;
ALTER TABLE import_history ADD COLUMN download_id TEXT DEFAULT NULL;

CREATE INDEX idx_queue_download_id ON import_queue(download_id);
CREATE INDEX idx_history_download_id ON import_history(download_id);

-- +goose Down
DROP INDEX IF EXISTS idx_history_download_id;
DROP INDEX IF EXISTS idx_queue_download_id;
