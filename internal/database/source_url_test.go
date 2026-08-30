package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetQueueItemBySourceURL(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	setupQueueSchema(t, db)

	metadataWithURL := `{"source_url":"https://indexer.com/api?id=123"}`
	_, err = db.Exec(`
		INSERT INTO import_queue (id, download_id, nzb_path, status, priority, metadata)
		VALUES (1, 'guid-123', '/tmp/test.nzb', 'pending', 1, ?)
	`, metadataWithURL)
	require.NoError(t, err)

	repo := NewRepository(db, DialectSQLite)
	queueRepo := NewQueueRepository(db, DialectSQLite)

	ctx := context.Background()

	// Test Repository
	item, err := repo.GetQueueItemBySourceURL(ctx, "https://indexer.com/api?id=123")
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, int64(1), item.ID)
	assert.Equal(t, "guid-123", *item.DownloadID)

	// Test QueueRepository
	qItem, err := queueRepo.GetQueueItemBySourceURL(ctx, "https://indexer.com/api?id=123")
	require.NoError(t, err)
	require.NotNil(t, qItem)
	assert.Equal(t, int64(1), qItem.ID)
	assert.Equal(t, "guid-123", *qItem.DownloadID)

	// Non-existent URL
	missingItem, err := repo.GetQueueItemBySourceURL(ctx, "https://indexer.com/api?id=999")
	require.NoError(t, err)
	assert.Nil(t, missingItem)
}

func TestGetImportHistoryBySourceURL(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	setupQueueSchema(t, db)
	setupImportHistorySchema(t, db)
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS file_health (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL UNIQUE,
			library_path TEXT
		);
	`)
	require.NoError(t, err)

	repo := NewRepository(db, DialectSQLite)
	queueRepo := NewQueueRepository(db, DialectSQLite)

	metadata := `{"source_url":"https://indexer.com/api?id=456"}`
	downloadID := "hist-guid-456"
	category := "movies"
	history := &ImportHistory{
		DownloadID:  &downloadID,
		NzbName:     "Test Release",
		FileName:    "test.mkv",
		FileSize:    1024,
		VirtualPath: "/virtual/movies/test.mkv",
		Category:    &category,
		Metadata:    &metadata,
		CompletedAt: time.Now(),
	}

	err = repo.AddImportHistory(context.Background(), history)
	require.NoError(t, err)

	ctx := context.Background()

	// Test Repository lookup
	histItem, err := repo.GetImportHistoryBySourceURL(ctx, "https://indexer.com/api?id=456")
	require.NoError(t, err)
	require.NotNil(t, histItem)
	assert.Equal(t, "hist-guid-456", *histItem.DownloadID)
	assert.Equal(t, "Test Release", histItem.NzbName)

	// Test QueueRepository lookup
	qHistItem, err := queueRepo.GetImportHistoryBySourceURL(ctx, "https://indexer.com/api?id=456")
	require.NoError(t, err)
	require.NotNil(t, qHistItem)
	assert.Equal(t, "hist-guid-456", *qHistItem.DownloadID)

	// Non-existent URL
	missingHist, err := repo.GetImportHistoryBySourceURL(ctx, "https://indexer.com/api?id=nonexistent")
	require.NoError(t, err)
	assert.Nil(t, missingHist)
}
