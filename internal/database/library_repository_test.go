package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestLibraryPersistenceRetentionAndSharedAssets(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	setupQueueSchema(t, db)
	migration, err := os.ReadFile("migrations/sqlite/042_saved_library.sql")
	require.NoError(t, err)
	_, err = db.Exec(strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	repo := NewRepository(db, DialectSQLite)
	ctx := context.Background()
	_, err = db.Exec(`INSERT INTO import_queue(id,nzb_path,storage_path,download_id,status,completed_at) VALUES(1,'/source.nzb','/movies/title','stremio:1','completed',datetime('now','-10 days'))`)
	require.NoError(t, err)
	stale, err := repo.GetExpiredStremioQueueItems(ctx, 24)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	title := LibraryTitle{Type: "movie", IMDbID: "tt1234567", Name: "Title", Metadata: json.RawMessage(`{"id":"tt1234567","type":"movie","name":"Title"}`)}
	LibraryAssetMu.Lock()
	saved, err := repo.SaveLibraryRelease(ctx, 1, title, 0, 0)
	LibraryAssetMu.Unlock()
	require.NoError(t, err)
	require.True(t, repo.LibraryAssetProtected(ctx, stale[0].NzbPath, ""), "stale cleanup selection must recheck")
	require.True(t, repo.LibraryAssetProtected(ctx, "other.nzb", "/movies/title/episode.mkv"), "shared subtree")
	require.True(t, repo.LibraryAssetProtected(ctx, "other.nzb", "/movies"), "ancestor deletion")
	require.False(t, repo.LibraryAssetProtected(ctx, "other.nzb", "/movies/title-other"))
	_, err = db.Exec(`DELETE FROM import_queue`)
	require.NoError(t, err)
	titles, err := repo.LibraryTitles(ctx)
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.Len(t, titles[0].Releases, 1)
	require.Equal(t, "/movies/title", *titles[0].Releases[0].Snapshot.StoragePath)
	unsaved, err := repo.UnsaveLibraryRelease(ctx, saved.ID, time.Hour)
	require.NoError(t, err)
	require.False(t, unsaved.Saved)
	require.GreaterOrEqual(t, unsaved.ExpiresAt, time.Now().Add(23*time.Hour).Unix())
	require.True(t, repo.LibraryAssetProtected(ctx, "/source.nzb", ""))
	titles, err = repo.LibraryTitles(ctx)
	require.NoError(t, err)
	require.Empty(t, titles)
	_, err = db.Exec(`UPDATE library_releases SET expires_at=1`)
	require.NoError(t, err)
	require.False(t, repo.LibraryAssetProtected(ctx, "/source.nzb", ""))
	require.NoError(t, repo.DeleteExpiredLibraryReference(ctx, saved.ID))
	refs, err := repo.LibraryReleases(ctx)
	require.NoError(t, err)
	require.Empty(t, refs)
}

func TestLibraryHealthByDownloadIDsAndMovieNormalization(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	setupQueueSchema(t, db)
	migration, err := os.ReadFile("migrations/sqlite/042_saved_library.sql")
	require.NoError(t, err)
	_, err = db.Exec(strings.Split(string(migration), "-- +goose Down")[0])
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE file_health (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		download_id TEXT DEFAULT NULL,
		status TEXT NOT NULL DEFAULT 'pending'
	)`)
	require.NoError(t, err)
	repo := NewRepository(db, DialectSQLite)
	healthRepo := NewHealthRepository(db, DialectSQLite)
	ctx := context.Background()

	_, err = db.Exec(`INSERT INTO file_health (file_path, download_id, status) VALUES
		('a.mkv', 'dl-1', 'healthy'),
		('b.mkv', 'dl-1', 'degraded'),
		('c.mkv', 'dl-2', 'corrupted'),
		('d.mkv', NULL, 'healthy')`)
	require.NoError(t, err)

	statuses, err := healthRepo.GetHealthStatusByDownloadIDs(ctx, []string{"dl-1", "dl-2", "dl-3", "", "dl-1"})
	require.NoError(t, err)
	require.Equal(t, HealthStatusDegraded, statuses["dl-1"], "worst status wins")
	require.Equal(t, HealthStatusCorrupted, statuses["dl-2"])
	require.NotContains(t, statuses, "dl-3")

	empty, err := healthRepo.GetHealthStatusByDownloadIDs(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)

	// Non-Stremio completed imports can be saved, and movies never keep S/E.
	_, err = db.Exec(`INSERT INTO import_queue(id,nzb_path,storage_path,download_id,status,completed_at) VALUES(7,'/arr.nzb','/series/show','arr-guid','completed',datetime('now'))`)
	require.NoError(t, err)
	title := LibraryTitle{Type: "movie", IMDbID: "tt1234567", Name: "Title", Metadata: json.RawMessage(`{"id":"tt1234567","type":"movie","name":"Title"}`)}
	LibraryAssetMu.Lock()
	saved, err := repo.SaveLibraryRelease(ctx, 7, title, 2, 3)
	LibraryAssetMu.Unlock()
	require.NoError(t, err)
	require.Zero(t, saved.Season)
	require.Zero(t, saved.Episode)
}
