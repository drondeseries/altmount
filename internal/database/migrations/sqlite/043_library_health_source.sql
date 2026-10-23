-- +goose Up
ALTER TABLE library_releases ADD COLUMN source TEXT NOT NULL DEFAULT 'queue';
ALTER TABLE library_releases ADD COLUMN file_health_id BIGINT DEFAULT NULL;
ALTER TABLE library_releases ADD COLUMN file_path TEXT DEFAULT NULL;
CREATE INDEX IF NOT EXISTS library_releases_health ON library_releases(source, file_health_id);

-- +goose Down
DROP INDEX IF EXISTS library_releases_health;
ALTER TABLE library_releases DROP COLUMN file_path;
ALTER TABLE library_releases DROP COLUMN file_health_id;
ALTER TABLE library_releases DROP COLUMN source;
