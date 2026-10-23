package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/kipsilabs/altmount/internal/database"
	"github.com/stretchr/testify/require"
)

func TestLibraryAddonAuthRequired(t *testing.T) {
	s := &Server{}
	app := fiber.New()
	app.Get("/catalog/:type/:catalog.json", s.handleLibraryCatalog)
	app.Get("/meta/:type/:id.json", s.handleLibraryMeta)
	for _, path := range []string{"/catalog/movie/altmount-saved.json", "/meta/movie/tt1234567.json"} {
		response, err := app.Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		require.Equal(t, 401, response.StatusCode)
		response.Body.Close()
	}
}
func TestLibraryMetadataFallbackValidation(t *testing.T) {
	raw := `{"imdb_id":"tt1234567","type":"series","season":2,"episode":3}`
	title, _, ok := libraryTitleForItem(&database.ImportQueueItem{NzbPath: "/nzb/Example.nzb", Metadata: &raw})
	require.True(t, ok)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(title.Metadata, &meta))
	require.Equal(t, "Example", meta["name"])
	require.Len(t, meta["videos"], 1)
	for _, bad := range []string{`{}`, `{"imdb_id":"../../bad","type":"movie"}`, `{"imdb_id":"tt1234567","type":"other"}`, `invalid`} {
		_, _, ok := libraryTitleForItem(&database.ImportQueueItem{Metadata: &bad})
		require.False(t, ok)
	}
}

func TestLibraryRecentStremioOnlyKeepsStremio(t *testing.T) {
	// Recent tab is a release-level policy: keep ALL Stremio-origin recents
	// (overlap survives, unverified survives); ARR/manual excluded.
	mk := func(origin, mediaType, imdb string, season, episode int) database.LibraryRelease {
		return database.LibraryRelease{
			Origin: origin, Type: mediaType, IMDbID: imdb,
			Season: season, Episode: episode, Name: origin + ":" + imdb,
		}
	}
	out := []database.LibraryRelease{
		mk(librarySourceStremio, "movie", "tt1000001", 0, 0),
		mk(librarySourceStremio, "movie", "tt1000002", 0, 0),
		mk(librarySourceArrs, "movie", "tt1000002", 0, 0),
		mk(librarySourceStremio, "series", "tt2000001", 1, 2),
		mk("manual", "series", "tt2000001", 1, 2),
		mk(librarySourceStremio, "series", "tt2000002", 0, 0), // unverified survives in recent
		mk("manual", "series", "tt2000002", 0, 0),
		mk(librarySourceStremio, "series", "tt2000003", 2, 1),
	}
	kept := out[:0]
	for _, r := range out {
		if r.Origin != librarySourceStremio {
			continue
		}
		kept = append(kept, r)
	}
	require.Len(t, kept, 5)
	require.Equal(t, "tt1000001", kept[0].IMDbID)
	require.Equal(t, "tt1000002", kept[1].IMDbID)
	require.Equal(t, "tt2000001", kept[2].IMDbID)
	require.Equal(t, "tt2000002", kept[3].IMDbID)
	require.Equal(t, "tt2000003", kept[4].IMDbID)
}

func TestLibraryStremioAnchor(t *testing.T) {
	anchor := func(mediaType, imdb string, origin string, season, episode int) bool {
		return libraryStremioAnchor(mediaType, imdb, database.LibraryRelease{Origin: origin, Season: season, Episode: episode})
	}
	require.True(t, anchor("movie", "tt0133093", "stremio", 0, 0))
	require.True(t, anchor("series", "tt0903747", "stremio", 2, 3))
	require.False(t, anchor("series", "tt0903747", "stremio", 0, 0), "unverified never anchors")
	require.False(t, anchor("movie", "altm-movie-x", "stremio", 0, 0), "synthetic never anchors")
	require.False(t, anchor("movie", "tt0133093", "arrs", 0, 0), "ARR never anchors")
	require.False(t, anchor("movie", "tt0133093", "manual", 0, 0))
	require.False(t, anchor("other", "tt0133093", "stremio", 0, 0))
	require.False(t, anchor("movie", "bad-id", "stremio", 0, 0))
}
