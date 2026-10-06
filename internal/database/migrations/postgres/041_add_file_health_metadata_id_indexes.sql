-- +goose Up
-- +goose StatementBegin
-- FindHealthyFilesForMovie / FindHealthyFilesForSeries look up healthy
-- file_health rows by the TMDB/TVDB ID stored inside the metadata JSONB.
-- The previous prefilter (CAST(metadata AS text) LIKE '%"tmdbId":123%')
-- cannot use any index, so every lookup scanned the whole table. These
-- expression indexes let PostgreSQL satisfy the exact text predicate
-- (metadata->>'tmdbId') = '123' with an index scan. The comparison is on
-- the extracted text so both numeric ("tmdbId":123) and string
-- ("tmdbId":"123") JSON shapes are index candidates. Nested shapes
-- (movie.tmdbId / series.tvdbId) have no index and are resolved by the Go
-- post-filter for rows reached through other paths; the Go post-filter
-- remains the authoritative exact match.
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_tmdb_id ON file_health(((metadata->>'tmdbId')));
CREATE INDEX IF NOT EXISTS idx_file_health_metadata_tvdb_id ON file_health(((metadata->>'tvdbId')));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_file_health_metadata_tmdb_id;
DROP INDEX IF EXISTS idx_file_health_metadata_tvdb_id;
-- +goose StatementEnd
