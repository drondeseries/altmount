package health

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kipsilabs/altmount/internal/arrs/model"
	"github.com/kipsilabs/altmount/internal/database"
	"github.com/stretchr/testify/require"
)

type metadataDiscoveryService struct{}

func (metadataDiscoveryService) TriggerFileRescan(context.Context, string, string, *string) error {
	return nil
}
func (metadataDiscoveryService) DiscoverFileMetadata(context.Context, string, string, string, string) (*model.WebhookMetadata, error) {
	return &model.WebhookMetadata{Movie: &model.MovieMetadata{Id: 1, TmdbId: 456}}, nil
}

func TestEnsureMetadataDiscoversInvalidStoredIdentifiers(t *testing.T) {
	for _, metadata := range []string{`{"tmdbId":null}`, `{"tvdbId":0}`, `{"tmdbId":"123"}`, `{"tvdbId":123.9}`, `{}`, `broken`} {
		t.Run(metadata, func(t *testing.T) {
			repo, db := setupWorkerTestDB(t)
			_, err := db.Exec("INSERT INTO file_health (file_path, status, metadata) VALUES (?, 'healthy', ?)", "movie.mkv", metadata)
			require.NoError(t, err)
			item, err := repo.GetFileHealth(context.Background(), "movie.mkv")
			require.NoError(t, err)
			worker := &HealthWorker{healthRepo: repo, arrsService: metadataDiscoveryService{}}
			got := worker.ensureMetadata(context.Background(), item)
			require.NotNil(t, got)
			var discovered model.WebhookMetadata
			require.NoError(t, json.Unmarshal([]byte(*got), &discovered))
			require.NotNil(t, discovered.Movie)
			require.Equal(t, int64(456), discovered.Movie.TmdbId)
			latest, err := repo.GetFileHealth(context.Background(), item.FilePath)
			require.NoError(t, err)
			require.Equal(t, got, latest.Metadata)
		})
	}
}

func TestEnsureMetadataReusesValidIdentifiers(t *testing.T) {
	for _, metadata := range []string{`{"tmdbId":123}`, `{"tvdb_id":123}`, `{"movie":{"tmdbId":123}}`, `{"series":{"tvdbId":123},"episodes":[{"id":1}]}`} {
		t.Run(metadata, func(t *testing.T) {
			// The fast path needs no repository or discovery service.
			worker := &HealthWorker{}
			got := worker.ensureMetadata(context.Background(), &database.FileHealth{Metadata: &metadata})
			require.Equal(t, &metadata, got)
		})
	}
}

func TestEnsureMetadataReusesConcurrentDiscovery(t *testing.T) {
	repo, db := setupWorkerTestDB(t)
	metadata := `{"tmdbId":123}`
	_, err := db.Exec("INSERT INTO file_health (file_path, status, metadata) VALUES (?, 'healthy', ?)", "movie.mkv", metadata)
	require.NoError(t, err)
	worker := &HealthWorker{healthRepo: repo}
	// The in-memory item predates metadata written by another worker.
	got := worker.ensureMetadata(context.Background(), &database.FileHealth{FilePath: "movie.mkv"})
	require.Equal(t, &metadata, got)
}
