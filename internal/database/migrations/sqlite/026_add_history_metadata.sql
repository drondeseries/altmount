-- +goose Up
ALTER TABLE import_history ADD COLUMN metadata TEXT DEFAULT NULL;

-- +goose Down
ALTER TABLE import_history DROP COLUMN metadata;
