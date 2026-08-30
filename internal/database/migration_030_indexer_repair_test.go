package database

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openSQLiteMigratedToPath opens a SQLite DB at the given path and runs migrations up to version.
func openSQLiteMigratedToPath(t *testing.T, dbPath string, version int64) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)

	goose.SetBaseFS(embedMigrations)
	require.NoError(t, goose.SetDialect("sqlite3"))
	goose.SetLogger(goose.NopLogger())
	require.NoError(t, goose.UpTo(db, "migrations/sqlite", version))
	return db
}

func requireSQLiteIndex(t *testing.T, db *sql.DB, indexName string) {
	t.Helper()
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", indexName).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "expected index %s to exist exactly once", indexName)
}

// TestMigration030IndexerRepair_CompleteMissingSchema is the regression guard for
// the startup failure users hit when jumping between release and dev images whose
// migration numbering collided: goose records migration 030 (indexer_import_stats) as
// applied, but the indexer columns and table never make it into the schema.
// Later migrations that copy file_health (033_add_degraded_status.sql) then die
// with "no such column: indexer".
func TestMigration030IndexerRepair_CompleteMissingSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "broken030.db")

	// 1. Migrate a SQLite database up to version 29.
	db := openSQLiteMigratedToPath(t, dbPath, 29)

	// 2. Simulate collision: mark migration 030 as applied without running its SQL.
	_, err := db.Exec(
		"INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (30, 1, CURRENT_TIMESTAMP)",
	)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// 3. Prove direct Goose migration to 033 fails before the fix.
	broken, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	broken.SetMaxOpenConns(1)
	err = goose.UpTo(broken, "migrations/sqlite", 33)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no such column: indexer")

	// Verify rollback left no orphan table behind.
	var orphan int
	require.NoError(t, broken.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='file_health_new'",
	).Scan(&orphan))
	require.Zero(t, orphan, "failed migration 033 must roll back completely")
	require.NoError(t, broken.Close())

	// 4. Opening through NewDB must now succeed via pre-migration repair.
	app, err := NewDB(Config{Type: "sqlite", DatabasePath: dbPath})
	require.NoError(t, err)
	defer app.Close()
	conn := app.Connection()

	// 5. Verify migration 030 schema is fully restored.
	require.True(t, hasColumn(conn, DialectSQLite, "file_health", "indexer"))
	require.True(t, hasColumn(conn, DialectSQLite, "import_queue", "indexer"))
	require.True(t, hasColumn(conn, DialectSQLite, "import_history", "indexer"))
	require.True(t, hasTable(conn, DialectSQLite, "indexer_import_stats"))
	require.True(t, hasColumn(conn, DialectSQLite, "indexer_import_stats", "download_id"))

	requireSQLiteIndex(t, conn, "idx_file_health_indexer")
	requireSQLiteIndex(t, conn, "idx_import_queue_indexer")
	requireSQLiteIndex(t, conn, "idx_import_history_indexer")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_name")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_created")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_download_id")

	// 6. Verify migration 033 ran: accepting 'degraded' status.
	_, err = conn.Exec("INSERT INTO file_health (file_path, status) VALUES ('/movies/a.mkv', 'degraded')")
	require.NoError(t, err)

	// 7. Full migration chain applied.
	var appliedMax int64
	require.NoError(t, conn.QueryRow("SELECT MAX(version_id) FROM goose_db_version").Scan(&appliedMax))
	require.Equal(t, maxEmbeddedSQLiteVersion(t), appliedMax, "full migration chain must be applied")
}

// TestMigration030IndexerRepair_HistoricalStatsWithoutDownloadID verifies that
// early historical variants of migration 030 (which created indexer_import_stats
// without the download_id column) are healed cleanly before creating indexes.
func TestMigration030IndexerRepair_HistoricalStatsWithoutDownloadID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "historical030.db")

	// 1. Migrate up to version 29.
	db := openSQLiteMigratedToPath(t, dbPath, 29)

	// 2. Manually create the early historical indexer_import_stats table (no download_id column).
	_, err := db.Exec(`
		CREATE TABLE indexer_import_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			indexer TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('success', 'failed')),
			error_message TEXT DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX idx_indexer_stats_name ON indexer_import_stats(indexer);
		CREATE INDEX idx_indexer_stats_created ON indexer_import_stats(created_at);

		ALTER TABLE import_queue ADD COLUMN indexer TEXT DEFAULT NULL;
		ALTER TABLE import_history ADD COLUMN indexer TEXT DEFAULT NULL;
		ALTER TABLE file_health ADD COLUMN indexer TEXT DEFAULT NULL;

		CREATE INDEX idx_import_queue_indexer ON import_queue(indexer);
		CREATE INDEX idx_import_history_indexer ON import_history(indexer);
		CREATE INDEX idx_file_health_indexer ON file_health(indexer);

		INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (30, 1, CURRENT_TIMESTAMP);
	`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// 3. Open through NewDB: must heal missing download_id and idx_indexer_stats_download_id.
	app, err := NewDB(Config{Type: "sqlite", DatabasePath: dbPath})
	require.NoError(t, err)
	defer app.Close()
	conn := app.Connection()

	// 4. Verify download_id column and index exist.
	require.True(t, hasColumn(conn, DialectSQLite, "indexer_import_stats", "download_id"))
	requireSQLiteIndex(t, conn, "idx_indexer_stats_download_id")

	// 5. Verify inserting with download_id works.
	_, err = conn.Exec(`
		INSERT INTO indexer_import_stats (indexer, status, download_id)
		VALUES ('test-indexer', 'success', 'dl-12345')
	`)
	require.NoError(t, err)

	// 6. Migration chain succeeded through latest version.
	var appliedMax int64
	require.NoError(t, conn.QueryRow("SELECT MAX(version_id) FROM goose_db_version").Scan(&appliedMax))
	require.Equal(t, maxEmbeddedSQLiteVersion(t), appliedMax)
}

// TestMigration030IndexerRepair_ExistingColumnsMissingIndexes verifies that
// if indexer columns already exist but indexes are missing, the repair
// recreates all missing indexes without error.
func TestMigration030IndexerRepair_ExistingColumnsMissingIndexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing_indexes.db")

	// 1. Migrate up to version 29.
	db := openSQLiteMigratedToPath(t, dbPath, 29)

	// 2. Add columns manually without creating indexes, and mark migration 30 applied.
	_, err := db.Exec(`
		ALTER TABLE import_queue ADD COLUMN indexer TEXT DEFAULT NULL;
		ALTER TABLE import_history ADD COLUMN indexer TEXT DEFAULT NULL;
		ALTER TABLE file_health ADD COLUMN indexer TEXT DEFAULT NULL;

		INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (30, 1, CURRENT_TIMESTAMP);
	`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// 3. Open through NewDB.
	app, err := NewDB(Config{Type: "sqlite", DatabasePath: dbPath})
	require.NoError(t, err)
	defer app.Close()
	conn := app.Connection()

	// 4. Verify all indexes were created despite the columns already existing before startup.
	requireSQLiteIndex(t, conn, "idx_import_queue_indexer")
	requireSQLiteIndex(t, conn, "idx_import_history_indexer")
	requireSQLiteIndex(t, conn, "idx_file_health_indexer")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_name")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_created")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_download_id")
}

// TestMigration030IndexerRepair_IdempotentSecondStartup verifies that opening
// an already-repaired and migrated database a second time is a clean no-op.
func TestMigration030IndexerRepair_IdempotentSecondStartup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idempotent.db")

	// 1. Migrate up to 29 and corrupt migration 30.
	db := openSQLiteMigratedToPath(t, dbPath, 29)
	_, err := db.Exec("INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (30, 1, CURRENT_TIMESTAMP)")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// 2. First startup: repairs and completes all migrations.
	app1, err := NewDB(Config{Type: "sqlite", DatabasePath: dbPath})
	require.NoError(t, err)
	require.NoError(t, app1.Close())

	// 3. Second startup: must succeed cleanly without schema conflict.
	app2, err := NewDB(Config{Type: "sqlite", DatabasePath: dbPath})
	require.NoError(t, err)
	defer app2.Close()
	conn := app2.Connection()

	// Verify indexes exist exactly once.
	requireSQLiteIndex(t, conn, "idx_import_queue_indexer")
	requireSQLiteIndex(t, conn, "idx_import_history_indexer")
	requireSQLiteIndex(t, conn, "idx_file_health_indexer")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_name")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_created")
	requireSQLiteIndex(t, conn, "idx_indexer_stats_download_id")
}

// TestMigration030IndexerRepair_FreshDatabaseUntouched verifies that a brand new
// database initializes cleanly through Goose without triggering premature repair.
func TestMigration030IndexerRepair_FreshDatabaseUntouched(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh.db")

	app, err := NewDB(Config{Type: "sqlite", DatabasePath: dbPath})
	require.NoError(t, err)
	defer app.Close()
	conn := app.Connection()

	var appliedMax int64
	require.NoError(t, conn.QueryRow("SELECT MAX(version_id) FROM goose_db_version").Scan(&appliedMax))
	require.Equal(t, maxEmbeddedSQLiteVersion(t), appliedMax)
}

// maxEmbeddedSQLiteVersion returns the highest version number available in the
// embedded SQLite migrations directory, keeping the assertion above future-proof.
func maxEmbeddedSQLiteVersion(t *testing.T) int64 {
	t.Helper()

	entries, err := embedMigrations.ReadDir("migrations/sqlite")
	require.NoError(t, err)

	var max int64
	for _, entry := range entries {
		name := entry.Name()
		idx := strings.IndexByte(name, '_')
		if idx < 0 {
			continue
		}
		v, err := strconv.ParseInt(name[:idx], 10, 64)
		if err == nil && v > max {
			max = v
		}
	}
	return max
}
