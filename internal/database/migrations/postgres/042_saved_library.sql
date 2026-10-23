-- +goose Up
CREATE TABLE library_titles (
    type TEXT NOT NULL,
    imdb_id TEXT NOT NULL,
    name TEXT NOT NULL,
    metadata TEXT NOT NULL,
    PRIMARY KEY (type, imdb_id)
);
CREATE TABLE library_releases (
    id BIGSERIAL PRIMARY KEY,
    type TEXT NOT NULL,
    imdb_id TEXT NOT NULL,
    queue_id BIGINT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    season INTEGER NOT NULL DEFAULT 0,
    episode INTEGER NOT NULL DEFAULT 0,
    saved INTEGER NOT NULL DEFAULT 1,
    expires_at BIGINT NOT NULL DEFAULT 0,
    nzb_path TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    download_id TEXT NOT NULL,
    snapshot TEXT NOT NULL,
    FOREIGN KEY (type, imdb_id) REFERENCES library_titles(type, imdb_id)
);
CREATE INDEX library_releases_title ON library_releases(type, imdb_id, saved);
-- +goose Down
DROP TABLE library_releases;
DROP TABLE library_titles;
