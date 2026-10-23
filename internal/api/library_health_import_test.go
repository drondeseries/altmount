package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kipsilabs/altmount/internal/database"
	"github.com/stretchr/testify/require"
)

func healthRecord(id int64, filePath, libraryPath, metadata string) *database.FileHealth {
	rec := &database.FileHealth{
		ID:       id,
		FilePath: filePath,
		Status:   database.HealthStatusHealthy,
	}
	if libraryPath != "" {
		rec.LibraryPath = &libraryPath
	}
	if metadata != "" {
		rec.Metadata = &metadata
	}
	return rec
}

func TestHealthTitleForMovieFilename(t *testing.T) {
	rec := healthRecord(1,
		"Gladiator II (2024) - [Remux-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-BR-GuyZo.mkv",
		"/movies/Gladiator II (2024)/Gladiator II (2024) - [Remux-2160p][TrueHD Atmos 7.1][DV HDR10][HEVC]-BR-GuyZo.mkv",
		"")
	title, season, episode, ok := healthTitleForRecord(context.Background(), rec, "test-agent")
	require.True(t, ok)
	require.Equal(t, "movie", title.Type)
	require.True(t, strings.HasPrefix(title.IMDbID, "altm-movie-"))
	require.NotContains(t, title.IMDbID, ":")
	require.Contains(t, title.Name, "Gladiator II")
	require.Zero(t, season)
	require.Zero(t, episode)
}

func TestHealthTitleForArrMovieTMDB(t *testing.T) {
	rec := healthRecord(2, "whiplash-remux.mkv", "/movies/Whiplash (2014)/whiplash-remux.mkv",
		`{"movie":{"tmdbId":244786}}`)
	title, season, episode, ok := healthTitleForRecord(context.Background(), rec, "test-agent")
	require.True(t, ok)
	require.Equal(t, "movie", title.Type)
	require.Equal(t, "altm-movie-tmdb-244786", title.IMDbID)
	require.Zero(t, season)
	require.Zero(t, episode)
}

func TestHealthTitleForArrSeriesTVDB(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":66,"name":"Breaking Bad","externals":{"imdb":"tt0903747","thetvdb":81189}}`))
	}))
	defer srv.Close()
	restore := overrideTVmazeBaseURL(srv.URL)
	defer restore()
	tvdbIMDbCache.reset()

	rec := healthRecord(3, "breaking.bad.s01e01.mkv", "/series/Breaking Bad/Season 1/breaking.bad.s01e01.mkv",
		`{"series":{"tvdbId":81189}}`)
	title, season, episode, ok := healthTitleForRecord(context.Background(), rec, "test-agent")
	require.True(t, ok)
	require.Equal(t, "series", title.Type)
	require.Equal(t, "tt0903747", title.IMDbID)
	require.Equal(t, "Breaking Bad", title.Name)
	require.Equal(t, 1, season)
	require.Equal(t, 1, episode)

	// Second resolution is served from cache: no new outbound request.
	rec2 := healthRecord(4, "breaking.bad.s01e02.mkv", "", `{"series":{"tvdbId":81189}}`)
	_, _, _, ok = healthTitleForRecord(context.Background(), rec2, "test-agent")
	require.True(t, ok)
	require.Equal(t, int64(1), atomic.LoadInt64(&calls))
}

func TestHealthTitleForSeriesFilename(t *testing.T) {
	rec := healthRecord(5, "Hokum.2026.S01E02.2160p.mkv", "", "")
	title, season, episode, ok := healthTitleForRecord(context.Background(), rec, "test-agent")
	require.True(t, ok)
	require.Equal(t, "series", title.Type)
	require.True(t, strings.HasPrefix(title.IMDbID, "altm-series-"))
	require.Equal(t, 1, season)
	require.Equal(t, 2, episode)
}

func TestHealthIdentityPrefersStremioIMDb(t *testing.T) {
	rec := healthRecord(10, "00012.m2ts", "",
		`{"imdb_id":"tt32639175","type":"movie","tmdbId":1309083}`)
	id, ok := healthIdentityForRecord(rec)
	require.True(t, ok)
	require.Equal(t, "movie", id.mediaType)
	require.Equal(t, "tt32639175", id.imdbID)
	title, _, _, ok := healthTitleForRecord(context.Background(), rec, "test-agent")
	require.True(t, ok)
	require.Equal(t, "tt32639175", title.IMDbID)
}

func TestHealthGroupKey(t *testing.T) {
	dl := "stremio:abc"
	movieID := healthIdentity{mediaType: "movie", name: "00012"}
	ep1 := healthIdentity{mediaType: "series", name: "Show", season: 1, episode: 1}
	ep2 := healthIdentity{mediaType: "series", name: "Show", season: 1, episode: 2}
	pack := healthIdentity{mediaType: "series", name: "Show"}
	rec := func() *database.FileHealth {
		d := dl
		return &database.FileHealth{ID: 1, FilePath: "/x.mkv", DownloadID: &d}
	}
	// Disc segments of one movie download share a group.
	a, _ := healthIdentityForRecord(healthRecord(1, "00011.m2ts", "", ""))
	b, _ := healthIdentityForRecord(healthRecord(2, "00012.m2ts", "", ""))
	require.Equal(t,
		healthGroupKey(rec(), a, dl),
		healthGroupKey(rec(), b, dl))
	// Distinct episodes stay separate.
	require.NotEqual(t,
		healthGroupKey(rec(), ep1, dl),
		healthGroupKey(rec(), ep2, dl))
	// Season packs and movies collapse per download.
	require.Equal(t,
		healthGroupKey(rec(), pack, dl),
		healthGroupKey(rec(), movieID, dl))
}

func TestResolveIMDbFromTVDBMissCached(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	restore := overrideTVmazeBaseURL(srv.URL)
	defer restore()
	tvdbIMDbCache.reset()

	hit := resolveIMDbFromTVDB(context.Background(), 999999999, "test-agent")
	require.Empty(t, hit.IMDbID)
	hit = resolveIMDbFromTVDB(context.Background(), 999999999, "test-agent")
	require.Empty(t, hit.IMDbID)
	require.Equal(t, int64(1), atomic.LoadInt64(&calls))
}
