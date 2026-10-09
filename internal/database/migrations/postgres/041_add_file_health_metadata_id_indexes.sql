-- +goose Up
-- +goose StatementBegin
-- Exact metadata-ID lookups cover flat Stremio and nested ARR metadata,
-- including legacy snake_case keys. Index every OR branch using the same text
-- extraction expression as healthMetadataIDPredicate. Its JSON number guard
-- rejects strings, and the typed decoder validates the returned candidates.
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_tmdb_id ON file_health(((metadata->>'tmdbId')));
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_tvdb_id ON file_health(((metadata->>'tvdbId')));
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_legacy_tmdb_id ON file_health(((metadata->>'tmdb_id')));
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_legacy_tvdb_id ON file_health(((metadata->>'tvdb_id')));
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_movie_tmdb_id ON file_health(((metadata->'movie'->>'tmdbId')));
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_movie_legacy_tmdb_id ON file_health(((metadata->'movie'->>'tmdb_id')));
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_series_tvdb_id ON file_health(((metadata->'series'->>'tvdbId')));
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_series_legacy_tvdb_id ON file_health(((metadata->'series'->>'tvdb_id')));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_file_health_metadata_tmdb_id;
DROP INDEX IF EXISTS idx_file_health_metadata_tvdb_id;
DROP INDEX IF EXISTS idx_file_health_metadata_legacy_tmdb_id;
DROP INDEX IF EXISTS idx_file_health_metadata_legacy_tvdb_id;
DROP INDEX IF EXISTS idx_file_health_metadata_movie_tmdb_id;
DROP INDEX IF EXISTS idx_file_health_metadata_movie_legacy_tmdb_id;
DROP INDEX IF EXISTS idx_file_health_metadata_series_tvdb_id;
DROP INDEX IF EXISTS idx_file_health_metadata_series_legacy_tvdb_id;
-- +goose StatementEnd
