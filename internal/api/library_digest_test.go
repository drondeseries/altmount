package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/kipsilabs/altmount/internal/database"
	"github.com/stretchr/testify/require"
)

func mustDigestJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

func setupDigestRepo(t *testing.T) (*sql.DB, *database.Repository) {
	t.Helper()
	tdb, err := database.NewDB(database.Config{DatabasePath: filepath.Join(t.TempDir(), "digest.db")})
	require.NoError(t, err)
	t.Cleanup(func() { tdb.Close() })
	return tdb.Connection(), database.NewRepository(tdb.Connection(), database.DialectSQLite)
}
func TestLibraryDigestResponse(t *testing.T) {
	sx, r := setupDigestRepo(t)
	ctx := context.Background()
	// SaveLibraryRelease only accepts completed imports with storage paths.
	// Queue rows 1 and 2 back the two releases under tt0133093.
	for _, q := range []string{
		`INSERT INTO import_queue(id,nzb_path,storage_path,download_id,status,completed_at,file_size) VALUES(1,'/nzbs/1917.nzb','/movies/1917','stremio:11','completed',CURRENT_TIMESTAMP,1000000)`,
		`INSERT INTO import_queue(id,nzb_path,storage_path,download_id,status,completed_at,file_size) VALUES(2,'/nzbs/1917-x.nzb','/movies/1917','stremio:22','completed',CURRENT_TIMESTAMP,2000000)`,
	} {
		_, err := sx.ExecContext(ctx, q)
		require.NoError(t, err)
	}
	database.LibraryAssetMu.Lock()
	_, err := r.SaveLibraryRelease(ctx, 1, database.LibraryTitle{
		Type:   "movie",
		IMDbID: "tt0133093",
		Name:   "fallback name",
		Metadata: mustDigestJSON(t, map[string]any{
			"name":        "The Matrix",
			"poster":      "https://images.metahub.space/poster/small/tt0133093/img",
			"background":  "https://images.metahub.space/background/medium/tt0133093/img",
			"imdbRating":  "8.7",
			"releaseInfo": []string{"1999", "2023"},
			"genres":      []string{"Action", "Sci-Fi"},
		}),
	}, 0, 0)
	if err == nil {
		_, err = r.SaveLibraryRelease(ctx, 2, database.LibraryTitle{Type: "movie", IMDbID: "tt0133093", Name: "The Matrix"}, 0, 0)
	}
	database.LibraryAssetMu.Unlock()
	require.NoError(t, err)

	app := fiber.New()
	app.Get("/library", (&Server{queueRepo: r}).handleLibraryList)
	resp, err := app.Test(httptest.NewRequest("GET", "/library?stremio_only=1&digest=1", nil), -60000)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	var body struct {
		Data []libraryDigest `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Data, 1)
	d := body.Data[0]
	require.Equal(t, "movie", d.Type)
	require.Equal(t, "tt0133093", d.IMDbID)
	require.Equal(t, "The Matrix", d.Title)
	require.Equal(t, "1999,2023", d.Year)
	require.Equal(t, "8.7", d.Rating)
	require.Equal(t, []string{"Action", "Sci-Fi"}, d.Genres)
	require.Equal(t, "https://images.metahub.space/poster/small/tt0133093/img", d.Poster)
	require.Equal(t, int64(3_000_000), d.TotalSize)
	require.Equal(t, 2, d.ReleaseCount)
	require.GreaterOrEqual(t, d.MaxImportedAt, int64(0))
	require.GreaterOrEqual(t, d.MaxLastVerified, int64(0))
}

func TestLibraryDigestYearFallbacks(t *testing.T) {
	// Strings pass through; numbers stringify; null/zero fall to year.
	d := libraryDigestFromTitle(database.LibraryTitle{
		Type: "movie", IMDbID: "tt6", Metadata: mustDigestJSON(t, map[string]any{"releaseInfo": nil, "year": "2012"}),
	})
	require.Equal(t, "2012", d.Year, "null releaseInfo falls back to year")
	d = libraryDigestFromTitle(database.LibraryTitle{
		Type: "movie", IMDbID: "tt7", Metadata: mustDigestJSON(t, map[string]any{"releaseInfo": 0, "year": "2007"}),
	})
	require.Equal(t, "2007", d.Year, "numeric zero falls back to year")
	d = libraryDigestFromTitle(database.LibraryTitle{
		Type: "movie", IMDbID: "tt8", Metadata: mustDigestJSON(t, map[string]any{"releaseInfo": nil, "year": nil}),
	})
	require.Equal(t, "", d.Year, "all-null stays empty")
	d = libraryDigestFromTitle(database.LibraryTitle{
		Type: "movie", IMDbID: "tt1", Metadata: mustDigestJSON(t, map[string]any{"releaseInfo": "2009"}),
	})
	require.Equal(t, "2009", d.Year)
	d = libraryDigestFromTitle(database.LibraryTitle{
		Type: "movie", IMDbID: "tt2", Metadata: mustDigestJSON(t, map[string]any{"releaseInfo": 2020, "year": 2019}),
	})
	require.Equal(t, "2020", d.Year)
	d = libraryDigestFromTitle(database.LibraryTitle{
		Type: "movie", IMDbID: "tt3", Metadata: mustDigestJSON(t, map[string]any{"releaseInfo": map[string]any{"start": 2010}, "year": "2018"}),
	})
	require.Equal(t, "2018", d.Year, "objects fall back to year")
	d = libraryDigestFromTitle(database.LibraryTitle{
		Type: "movie", IMDbID: "tt4", Metadata: mustDigestJSON(t, map[string]any{"year": "2015"}),
	})
	require.Equal(t, "2015", d.Year, "missing releaseInfo falls back to year")
	d = libraryDigestFromTitle(database.LibraryTitle{
		Type: "movie", IMDbID: "tt5", Metadata: mustDigestJSON(t, map[string]any{"name": "Empty Year"}),
	})
	require.Equal(t, "", d.Year, "no year anywhere stays empty")
}
