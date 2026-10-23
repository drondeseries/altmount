package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestEnrichLibraryReleasesFromHistoryAndHealth(t *testing.T) {
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
		last_checked DATETIME,
		retry_count INTEGER NOT NULL DEFAULT 0,
		streaming_failure_count INTEGER NOT NULL DEFAULT 0,
		metadata TEXT DEFAULT NULL,
		download_id TEXT DEFAULT NULL,
		library_path TEXT DEFAULT NULL
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE import_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nzb_id INTEGER,
		nzb_name TEXT NOT NULL,
		file_name TEXT NOT NULL,
		file_size BIGINT,
		virtual_path TEXT NOT NULL,
		category TEXT,
		completed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		download_id TEXT DEFAULT NULL,
		status TEXT DEFAULT 'completed' NOT NULL,
		instance_name TEXT DEFAULT NULL,
		metadata TEXT DEFAULT NULL,
		indexer TEXT DEFAULT NULL
	)`)
	require.NoError(t, err)
	migration43, err := os.ReadFile("migrations/sqlite/043_library_health_source.sql")
	require.NoError(t, err)
	_, err = db.Exec(strings.Split(string(migration43), "-- +goose Down")[0])
	require.NoError(t, err)
	ctx := context.Background()
	repo := NewRepository(db, DialectSQLite)

	_, err = db.Exec(`INSERT INTO file_health(id,file_path,last_checked,retry_count,streaming_failure_count,metadata,library_path) VALUES(10,'/media/a.mkv','2026-01-02 03:04:05',2,1,'{"instanceName":"Radarr","movie":{"id":27,"tmdbId":13358},"movieFile":{"id":8563,"sceneName":"A.Dennis"}}','/mnt/usenet/movies/A Dennis (2007)/a.mkv')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO import_history(nzb_name,file_name,virtual_path,file_size,category,download_id,instance_name,indexer,completed_at,metadata)
		VALUES('a.nzb','a.mkv','/media/a.mkv',4096,'Movies','dl-x','Radarr-4K','NZBgeek','2026-01-01 00:00:00','{"scene_name":"A"}')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO import_queue(id,nzb_path,storage_path,download_id,status,completed_at,metadata,file_size)
		VALUES(1,'/a.nzb','/media','dl-x','completed','2026-01-01 00:00:00','{"imdb_id":"tt1234567","type":"movie"}',4096)`)
	require.NoError(t, err)

	title := LibraryTitle{Type: "movie", IMDbID: "tt1234567", Name: "A", Metadata: json.RawMessage(`{}`)}
	LibraryAssetMu.Lock()
	saved, err := repo.SaveLibraryRelease(ctx, 1, title, 0, 0)
	LibraryAssetMu.Unlock()
	require.NoError(t, err)

	healthTitle := LibraryTitle{Type: "movie", IMDbID: "altm-movie-tmdb-1", Name: "B", Metadata: json.RawMessage(`{}`)}
	LibraryAssetMu.Lock()
	imported, err := repo.ImportHealthRelease(ctx, 10, "/media/a.mkv", healthTitle, 0, 0, "dl-y", "", "")
	LibraryAssetMu.Unlock()
	require.NoError(t, err)

	releases := []LibraryRelease{*saved, *imported}
	repo.EnrichLibraryReleases(ctx, releases)

	require.Equal(t, "NZBgeek", releases[0].Indexer)
	require.Equal(t, "Radarr-4K", releases[0].Instance)
	require.Equal(t, "Movies", releases[0].Category)
	require.Equal(t, int64(4096), releases[0].Size)
	require.Equal(t, "A", releases[0].SceneName)
	require.Greater(t, releases[0].ImportedAt, int64(0))

	require.Equal(t, "dl-y", releases[1].DownloadID)
	require.Greater(t, releases[1].LastVerified, int64(0))
	require.Equal(t, 2, releases[1].RetryCount)
	require.Equal(t, 1, releases[1].StreamFails)
	require.Equal(t, "Radarr", releases[1].Instance)
	require.Equal(t, "A.Dennis", releases[1].SceneName)
	require.Equal(t, int64(13358), releases[1].TmdbID)
	require.Equal(t, "/mnt/usenet/movies/A Dennis (2007)/a.mkv", releases[1].Location)

	// No history rows: enrichment is a no-op, not an error.
	empty := []LibraryRelease{{ID: 999, DownloadID: "unknown"}}
	repo.EnrichLibraryReleases(ctx, empty)
	require.Empty(t, empty[0].Indexer)
}

func TestEnrichLibraryReleasesLegacyHistory(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	// Legacy import_history without instance_name (older production schema).
	_, err = db.Exec(`CREATE TABLE import_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nzb_name TEXT NOT NULL,
		file_name TEXT NOT NULL,
		virtual_path TEXT NOT NULL,
		file_size BIGINT,
		category TEXT,
		completed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		download_id TEXT DEFAULT NULL,
		status TEXT DEFAULT 'completed' NOT NULL,
		metadata TEXT DEFAULT NULL,
		indexer TEXT DEFAULT NULL
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO import_history(nzb_name,file_name,virtual_path,file_size,category,download_id,indexer,completed_at,metadata)
		VALUES('b.nzb','b.mkv','/media/b.mkv',2048,'TV','dl-legacy','OtherIndexer','2026-02-01 00:00:00','not json')`)
	require.NoError(t, err)
	repo := NewRepository(db, DialectSQLite)
	releases := []LibraryRelease{{ID: 1, DownloadID: "dl-legacy"}}
	repo.EnrichLibraryReleases(context.Background(), releases)
	require.Equal(t, "OtherIndexer", releases[0].Indexer)
	require.Empty(t, releases[0].Instance)
	require.Equal(t, "TV", releases[0].Category)
	require.Equal(t, int64(2048), releases[0].Size)
	require.Greater(t, releases[0].ImportedAt, int64(0))
	// Invalid metadata JSON must not poison the row.
	require.Empty(t, releases[0].SceneName)
}

func TestParseLibraryTimeAndSceneName(t *testing.T) {
	require.Equal(t, int64(0), parseLibraryTime(""))
	require.Equal(t, int64(0), parseLibraryTime("not a date"))
	require.Greater(t, parseLibraryTime("2026-01-01 00:00:00"), int64(0))
	require.Greater(t, parseLibraryTime("2026-01-01T00:00:00Z"), int64(0))
	require.Equal(t, "Scene", sceneNameFromMetadata(`{"scene_name":" Scene "}`))
	require.Empty(t, sceneNameFromMetadata(`{"other":1}`))
	require.Empty(t, sceneNameFromMetadata(`{invalid`))
	require.Empty(t, sceneNameFromMetadata(""))
}

func TestListHealthyLibraryFilesSkipsImported(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	setupQueueSchema(t, db)
	for _, name := range []string{"042_saved_library.sql", "043_library_health_source.sql"} {
		migration, err := os.ReadFile("migrations/sqlite/" + name)
		require.NoError(t, err)
		_, err = db.Exec(strings.Split(string(migration), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	_, err = db.Exec(`CREATE TABLE file_health (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		library_path TEXT DEFAULT NULL,
		metadata TEXT DEFAULT NULL,
		indexer TEXT DEFAULT NULL,
		download_id TEXT DEFAULT NULL,
		source_nzb_path TEXT DEFAULT NULL
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO file_health(file_path,status) VALUES('/a.mkv','healthy'),('/b.mkv','healthy'),('/c.mkv','corrupted')`)
	require.NoError(t, err)
	ctx := context.Background()
	repo := NewRepository(db, DialectSQLite)
	healthRepo := NewHealthRepository(db, DialectSQLite)

	listed, err := healthRepo.ListHealthyLibraryFiles(ctx, 100)
	require.NoError(t, err)
	require.Len(t, listed, 2)

	title := LibraryTitle{Type: "movie", IMDbID: "altm-movie-a", Name: "A", Metadata: json.RawMessage(`{}`)}
	LibraryAssetMu.Lock()
	_, err = repo.ImportHealthRelease(ctx, listed[0].ID, listed[0].FilePath, title, 0, 0, "", "", "")
	LibraryAssetMu.Unlock()
	require.NoError(t, err)

	listed, err = healthRepo.ListHealthyLibraryFiles(ctx, 100)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "/b.mkv", listed[0].FilePath)
}

func TestRelinkLibraryTitle(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	setupQueueSchema(t, db)
	for _, name := range []string{"042_saved_library.sql", "043_library_health_source.sql"} {
		migration, err := os.ReadFile("migrations/sqlite/" + name)
		require.NoError(t, err)
		_, err = db.Exec(strings.Split(string(migration), "-- +goose Down")[0])
		require.NoError(t, err)
	}
	ctx := context.Background()
	repo := NewRepository(db, DialectSQLite)
	synth := LibraryTitle{Type: "movie", IMDbID: "altm-movie-tmdb-584", Name: "2 Fast 2 Furious", Metadata: json.RawMessage(`{}`)}
	_, err = db.Exec(`INSERT INTO library_titles(type,imdb_id,name,metadata) VALUES(?,?,?,?)`,
		synth.Type, synth.IMDbID, synth.Name, string(synth.Metadata))
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO library_releases(type,imdb_id,queue_id,name,season,episode,nzb_path,storage_path,download_id,snapshot,source) VALUES(?,?,?,?,?,?,?,?,?,?,'queue')`,
		"movie", synth.IMDbID, 99, "2 Fast 2 Furious", 0, 0, "/x.nzb", "/m", "dl-1", "{}")
	require.NoError(t, err)

	resolved := LibraryTitle{Type: "movie", IMDbID: "tt0322259", Name: "2 Fast 2 Furious",
		Metadata: json.RawMessage(`{"id":"tt0322259","type":"movie","name":"2 Fast 2 Furious"}`)}
	require.NoError(t, repo.RelinkLibraryTitle(ctx, "movie", synth.IMDbID, resolved))

	refs, err := repo.LibraryReleases(ctx)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, "tt0322259", refs[0].IMDbID)
	require.Equal(t, "2 Fast 2 Furious", refs[0].Name)

	titles, err := repo.LibraryTitles(ctx)
	require.NoError(t, err)
	require.Len(t, titles, 1)
	require.Equal(t, "tt0322259", titles[0].IMDbID)

	require.Error(t, repo.RelinkLibraryTitle(ctx, "movie", "tt0322259", resolved))
	require.Error(t, repo.RelinkLibraryTitle(ctx, "", "tt0322259", resolved))
}
