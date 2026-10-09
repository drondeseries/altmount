package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthMetadataIDsAreExact(t *testing.T) {
	for _, tc := range []struct {
		metadata      string
		movie, series bool
	}{
		{`{"tmdbId":123,"tvdbId":123}`, true, true},
		{`{"movie":{"tmdb_id":123},"series":{"tvdb_id":123}}`, true, true},
		{`{"tmdbId":123.9,"tvdbId":123.9}`, false, false},
		{`{"tmdbId":1234,"tvdbId":1234}`, false, false},
		{`{"tmdbId":"123","tvdbId":"123"}`, false, false},
		{`{"tmdbId":null,"tvdbId":null}`, false, false},
		{`not json`, false, false},
	} {
		t.Run(tc.metadata, func(t *testing.T) {
			require.Equal(t, tc.movie, hasMatchingTMDBID(tc.metadata, 123))
			require.Equal(t, tc.series, hasMatchingTVDBID(tc.metadata, 123))
		})
	}
}

func TestHealthyMetadataLookupSupportsJSONShapes(t *testing.T) {
	testHealthyMetadataLookupSupportsJSONShapes(t, setupTestDB)
}

func TestHealthyMetadataLookupSupportsJSONShapes_Postgres(t *testing.T) {
	db := openPostgresLatest(t)
	testHealthyMetadataLookupSupportsJSONShapes(t, func(t *testing.T) *HealthRepository {
		_, err := db.Exec("DELETE FROM file_health")
		require.NoError(t, err)
		return NewHealthRepository(db, DialectPostgres)
	})
}

func testHealthyMetadataLookupSupportsJSONShapes(t *testing.T, setup func(*testing.T) *HealthRepository) {
	for _, tc := range []struct {
		name, metadata string
		movie          bool
	}{
		{"flat movie whitespace", `{"tmdbId": 123}`, true},
		{"nested movie whitespace", `{"movie": {"tmdbId": 123}}`, true},
		{"flat movie legacy", `{"tmdb_id":123}`, true},
		{"nested movie legacy", `{"movie":{"tmdb_id":123}}`, true},
		{"flat series whitespace", `{"tvdbId": 123}`, false},
		{"nested series whitespace", `{"series": {"tvdbId": 123}}`, false},
		{"flat series legacy", `{"tvdb_id":123}`, false},
		{"nested series legacy", `{"series":{"tvdb_id":123}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := setup(t)
			if !repo.dialect.IsPostgres() {
				t.Cleanup(func() { require.NoError(t, repo.db.db.Close()) })
			}
			ctx := context.Background()
			_, err := repo.db.ExecContext(ctx, "INSERT INTO file_health (file_path, status, metadata) VALUES (?, 'healthy', ?)", "wanted.mkv", tc.metadata)
			require.NoError(t, err)
			// Newer prefix matches must not consume the result limit ahead of the exact match.
			for i := 0; i < 105; i++ {
				_, err = repo.db.ExecContext(ctx, "INSERT INTO file_health (file_path, status, metadata) VALUES (?, 'healthy', ?)", fmt.Sprintf("decoy-%d.mkv", i), `{"tmdbId":1234,"tvdbId":1234}`)
				require.NoError(t, err)
				_, err = repo.db.ExecContext(ctx, "INSERT INTO file_health (file_path, status, metadata) VALUES (?, 'healthy', ?)", fmt.Sprintf("decimal-%d.mkv", i), `{"tmdbId":123.0,"tvdbId":123.0}`)
				require.NoError(t, err)
			}
			for _, metadata := range []string{`broken`, `{"tmdbId":"123","tvdbId":"123"}`, `{"tmdbId":123.9,"tvdbId":123.9}`, `{"other":{"tmdbId":123,"tvdbId":123}}`} {
				if metadata == "broken" && repo.dialect.IsPostgres() {
					continue
				} // JSONB rejects malformed input on write.
				_, err = repo.db.ExecContext(ctx, "INSERT INTO file_health (file_path, status, metadata) VALUES (?, 'healthy', ?)", metadata+".mkv", metadata)
				require.NoError(t, err)
			}
			var files []*FileHealth
			if tc.movie {
				files, err = repo.FindHealthyFilesForMovie(ctx, "", "", 123)
			} else {
				files, err = repo.FindHealthyFilesForSeries(ctx, "", 123)
			}
			require.NoError(t, err)
			require.Len(t, files, 1)
			require.Equal(t, "wanted.mkv", files[0].FilePath)
		})
	}
}
