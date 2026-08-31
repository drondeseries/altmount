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

	metadataWithURL := `{"source_url":"http://prowlarr:9696/download?link=proxy1","resolved_url":"https://indexer.com/api?id=123"}`
	_, err = db.Exec(`
		INSERT INTO import_queue (id, download_id, nzb_path, status, priority, metadata)
		VALUES (1, 'guid-123', '/tmp/test.nzb', 'pending', 1, ?)
	`, metadataWithURL)
	require.NoError(t, err)

	repo := NewRepository(db, DialectSQLite)
	queueRepo := NewQueueRepository(db, DialectSQLite)

	ctx := context.Background()

	// Test Repository with source_url
	item, err := repo.GetQueueItemBySourceURL(ctx, "http://prowlarr:9696/download?link=proxy1")
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, int64(1), item.ID)
	assert.Equal(t, "guid-123", *item.DownloadID)

	// Test Repository with resolved_url
	itemResolved, err := repo.GetQueueItemBySourceURL(ctx, "https://indexer.com/api?id=123")
	require.NoError(t, err)
	require.NotNil(t, itemResolved)
	assert.Equal(t, int64(1), itemResolved.ID)
	assert.Equal(t, "guid-123", *itemResolved.DownloadID)

	// Test QueueRepository with source_url
	qItem, err := queueRepo.GetQueueItemBySourceURL(ctx, "http://prowlarr:9696/download?link=proxy1")
	require.NoError(t, err)
	require.NotNil(t, qItem)
	assert.Equal(t, int64(1), qItem.ID)
	assert.Equal(t, "guid-123", *qItem.DownloadID)

	// Test QueueRepository with resolved_url
	qItemResolved, err := queueRepo.GetQueueItemBySourceURL(ctx, "https://indexer.com/api?id=123")
	require.NoError(t, err)
	require.NotNil(t, qItemResolved)
	assert.Equal(t, int64(1), qItemResolved.ID)
	assert.Equal(t, "guid-123", *qItemResolved.DownloadID)

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

	metadata := `{"source_url":"http://prowlarr:9696/download?link=proxy2","resolved_url":"https://indexer.com/api?id=456"}`
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

	// Test Repository lookup by source_url
	histItem, err := repo.GetImportHistoryBySourceURL(ctx, "http://prowlarr:9696/download?link=proxy2")
	require.NoError(t, err)
	require.NotNil(t, histItem)
	assert.Equal(t, "hist-guid-456", *histItem.DownloadID)
	assert.Equal(t, "Test Release", histItem.NzbName)

	// Test Repository lookup by resolved_url
	histItemResolved, err := repo.GetImportHistoryBySourceURL(ctx, "https://indexer.com/api?id=456")
	require.NoError(t, err)
	require.NotNil(t, histItemResolved)
	assert.Equal(t, "hist-guid-456", *histItemResolved.DownloadID)

	// Test QueueRepository lookup by resolved_url
	qHistItemResolved, err := queueRepo.GetImportHistoryBySourceURL(ctx, "https://indexer.com/api?id=456")
	require.NoError(t, err)
	require.NotNil(t, qHistItemResolved)
	assert.Equal(t, "hist-guid-456", *qHistItemResolved.DownloadID)

	// Non-existent URL
	missingHist, err := repo.GetImportHistoryBySourceURL(ctx, "https://indexer.com/api?id=nonexistent")
	require.NoError(t, err)
	assert.Nil(t, missingHist)
}
