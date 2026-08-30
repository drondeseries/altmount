package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/javi11/altmount/internal/config"
	"github.com/javi11/altmount/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSABnzbdAddUrl_SkipsDuplicateQueueItem(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.NewDB(database.Config{Type: "sqlite", DatabasePath: dbPath})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := database.NewRepository(db.Connection(), db.Dialect())

	sourceURL := "https://example.indexer/api?id=duplicate_test_1"
	downloadID := "existing-guid-123"
	metadata := `{"source_url":"` + sourceURL + `"}`
	item := &database.ImportQueueItem{
		DownloadID: &downloadID,
		NzbPath:    "/tmp/test.nzb",
		Status:     database.QueueStatusPending,
		Priority:   database.QueuePriorityNormal,
		Metadata:   &metadata,
	}
	require.NoError(t, repo.AddToQueue(context.Background(), item))

	server := &Server{
		queueRepo: repo,
	}

	app := fiber.New()
	app.Get("/api", server.handleSABnzbdAddUrl)

	req := httptest.NewRequest(http.MethodGet, "/api?name="+sourceURL, nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var addResp SABnzbdAddResponse
	require.NoError(t, json.Unmarshal(bodyBytes, &addResp))

	assert.True(t, addResp.Status)
	require.Len(t, addResp.NzoIds, 1)
	assert.Equal(t, downloadID, addResp.NzoIds[0])
}

func TestHandleSABnzbdAddUrl_SkipsDuplicateHistoryItem(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := database.NewDB(database.Config{Type: "sqlite", DatabasePath: dbPath})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := database.NewRepository(db.Connection(), db.Dialect())

	sourceURL := "https://example.indexer/api?id=duplicate_test_2"
	downloadID := "history-guid-456"
	metadata := `{"source_url":"` + sourceURL + `"}`
	category := "tv"
	history := &database.ImportHistory{
		DownloadID:  &downloadID,
		NzbName:     "Test.Show.S01E01",
		FileName:    "test.show.s01e01.mkv",
		FileSize:    2048,
		VirtualPath: "/virtual/tv/test.show.s01e01.mkv",
		Category:    &category,
		Metadata:    &metadata,
		CompletedAt: time.Now(),
	}
	require.NoError(t, repo.AddImportHistory(context.Background(), history))

	server := &Server{
		queueRepo: repo,
	}

	app := fiber.New()
	app.Get("/api", server.handleSABnzbdAddUrl)

	req := httptest.NewRequest(http.MethodGet, "/api?name="+sourceURL, nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var addResp SABnzbdAddResponse
	require.NoError(t, json.Unmarshal(bodyBytes, &addResp))

	assert.True(t, addResp.Status)
	require.Len(t, addResp.NzoIds, 1)
	assert.Equal(t, downloadID, addResp.NzoIds[0])
}

func TestHandleSABnzbdAddUrl_UserAgentPassthrough(t *testing.T) {
	var receivedUA string
	mockIndexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/x-nzb")
		w.Header().Set("Content-Disposition", `attachment; filename="release.nzb"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<nzb></nzb>"))
	}))
	defer mockIndexer.Close()

	server := &Server{}
	app := fiber.New()
	app.Get("/api", server.handleSABnzbdAddUrl)

	customUA := "Radarr/5.3.6.8612 (linux)"
	req := httptest.NewRequest(http.MethodGet, "/api?name="+mockIndexer.URL, nil)
	req.Header.Set("User-Agent", customUA)

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, customUA, receivedUA)
}

func TestHandleSABnzbdAddUrl_UserAgentDefaultFallback(t *testing.T) {
	var receivedUA string
	mockIndexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/x-nzb")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<nzb></nzb>"))
	}))
	defer mockIndexer.Close()

	server := &Server{}
	app := fiber.New()
	app.Get("/api", server.handleSABnzbdAddUrl)

	req := httptest.NewRequest(http.MethodGet, "/api?name="+mockIndexer.URL, nil)
	req.Header.Del("User-Agent")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "SABnzbd/4.4.1", receivedUA)
}

func TestHandleSABnzbdAddUrl_UserAgentConfiguredFallback(t *testing.T) {
	var receivedUA string
	mockIndexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/x-nzb")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<nzb></nzb>"))
	}))
	defer mockIndexer.Close()

	cfg := &config.Config{}
	cfg.SABnzbd.UserAgent = "CustomUserAgent/1.0"

	server := &Server{
		configManager: &mockConfigManager{cfg: cfg},
	}
	app := fiber.New()
	app.Get("/api", server.handleSABnzbdAddUrl)

	req := httptest.NewRequest(http.MethodGet, "/api?name="+mockIndexer.URL, nil)
	req.Header.Del("User-Agent")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "CustomUserAgent/1.0", receivedUA)
}
