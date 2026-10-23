package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kipsilabs/altmount/internal/database"
	"github.com/stretchr/testify/require"
)

func libraryStrPtr(s string) *string { return &s }

func TestClassifyLibraryItem(t *testing.T) {
	stremio := "stremio:uuid"
	require.Equal(t, librarySourceStremio, classifyLibraryItem(&database.ImportQueueItem{DownloadID: &stremio}))
	// Prefix wins even when ARR markers are present.
	require.Equal(t, librarySourceStremio, classifyLibraryItem(&database.ImportQueueItem{
		DownloadID: &stremio, Metadata: libraryStrPtr(`{"series_title":"Show"}`),
	}))
	guid := "550e8400-e29b-41d4-a716-446655440000"
	require.Equal(t, librarySourceArrs, classifyLibraryItem(&database.ImportQueueItem{
		DownloadID: &guid, Metadata: libraryStrPtr(`{"series_title":"Breaking Bad"}`),
	}))
	require.Equal(t, librarySourceArrs, classifyLibraryItem(&database.ImportQueueItem{
		Metadata: libraryStrPtr(`{"movie_title":"Whiplash"}`),
	}))
	require.Equal(t, librarySourceOther, classifyLibraryItem(&database.ImportQueueItem{}))
	require.Equal(t, librarySourceOther, classifyLibraryItem(&database.ImportQueueItem{
		DownloadID: &guid, Metadata: libraryStrPtr(`not json`),
	}))
	require.Equal(t, librarySourceOther, classifyLibraryItem(nil))
}

func TestLibraryAutoSaveScopeAllows(t *testing.T) {
	require.True(t, libraryAutoSaveScopeAllows("stremio", librarySourceStremio))
	require.False(t, libraryAutoSaveScopeAllows("stremio", librarySourceArrs))
	require.False(t, libraryAutoSaveScopeAllows("stremio", librarySourceOther))
	require.True(t, libraryAutoSaveScopeAllows("arrs", librarySourceArrs))
	require.False(t, libraryAutoSaveScopeAllows("arrs", librarySourceStremio))
	require.True(t, libraryAutoSaveScopeAllows("all", librarySourceOther))
	require.False(t, libraryAutoSaveScopeAllows("off", librarySourceStremio))
	require.False(t, libraryAutoSaveScopeAllows("bogus", librarySourceStremio))
}

func TestLibrarySlug(t *testing.T) {
	require.Equal(t, "breaking-bad", librarySlug("Breaking Bad"))
	require.Equal(t, "whiplash-2014-proper-bluray-1080p", librarySlug("Whiplash.2014.PROPER.BluRay.1080p"))
	require.Equal(t, "untitled", librarySlug("..."))
	require.NotContains(t, librarySlug("A/B: C"), ":")
}

func TestLibraryTitleForAutosave(t *testing.T) {
	// Stremio movie keeps its IMDb identity.
	dl := "stremio:uuid"
	title, season, episode, ok := libraryTitleForAutosave(&database.ImportQueueItem{
		NzbPath:    "/nzb/Whiplash.2014.nzb",
		DownloadID: &dl,
		Metadata:   libraryStrPtr(`{"imdb_id":"tt2582802","type":"movie"}`),
	})
	require.True(t, ok)
	require.Equal(t, "tt2582802", title.IMDbID)
	require.Equal(t, "movie", title.Type)
	require.Zero(t, season)
	require.Zero(t, episode)

	// ARR series title gets a colon-free synthetic ID and no episode info.
	title, season, episode, ok = libraryTitleForAutosave(&database.ImportQueueItem{
		NzbPath:  "/nzb/some.release.nzb",
		Metadata: libraryStrPtr(`{"series_title":"Breaking Bad"}`),
	})
	require.True(t, ok)
	require.Equal(t, "series", title.Type)
	require.Equal(t, "altm-series-breaking-bad", title.IMDbID)
	require.NotContains(t, title.IMDbID, ":")
	require.Zero(t, season)
	require.Zero(t, episode)

	// Generic SxxExx release name infers series + episode.
	title, season, episode, ok = libraryTitleForAutosave(&database.ImportQueueItem{
		NzbPath: "/nzb/Hokum.2026.S01E02.2160p.nzb",
	})
	require.True(t, ok)
	require.Equal(t, "series", title.Type)
	require.True(t, strings.HasPrefix(title.IMDbID, "altm-series-"))
	require.Equal(t, 1, season)
	require.Equal(t, 2, episode)

	// Generic movie name defaults to movie without episode info.
	title, season, episode, ok = libraryTitleForAutosave(&database.ImportQueueItem{
		NzbPath: "/nzb/Whiplash.2014.PROPER.BluRay.1080p.TrueHD.Atmos.7.1.AVC.HYBRID.REMUX-FraMeSToR.nzb",
	})
	require.True(t, ok)
	require.Equal(t, "movie", title.Type)
	require.True(t, strings.HasPrefix(title.IMDbID, "altm-movie-"))
	require.Zero(t, season)
	require.Zero(t, episode)

	// No name at all is not identifiable.
	_, _, _, ok = libraryTitleForAutosave(&database.ImportQueueItem{NzbPath: ""})
	require.False(t, ok)

	// Obfuscated NZB filename inside an informative parent folder derives
	// the title from the folder (1917 case): searchable, not a hash.
	title, _, _, ok = libraryTitleForAutosave(&database.ImportQueueItem{
		NzbPath: "/nzb/1917.2019.UHD.BluRay.2160p.TrueHD.Atmos.7.1.HEVC.REMUX-FraMeSToR/058c00564b21470bb99e042ac8b1d0b6.nzb",
	})
	require.True(t, ok)
	require.Equal(t, "movie", title.Type)
	require.Contains(t, title.Name, "1917")
	require.NotContains(t, title.Name, "058c00564b")
	require.True(t, strings.HasPrefix(title.IMDbID, "altm-movie-1917"))

	// Hash filename with no informative parent stays hash-named (review queue).
	title, _, _, ok = libraryTitleForAutosave(&database.ImportQueueItem{
		NzbPath: "/nzb/058c00564b21470bb99e042ac8b1d0b6.nzb",
	})
	require.True(t, ok)
	require.Contains(t, title.IMDbID, "058c00564b")
}

func TestReleaseOrigin(t *testing.T) {
	stremioDL := "stremio:uuid"
	arrMeta := `{"series_title":"Show"}`
	stremioMeta := `{"imdb_id":"tt1234567","type":"movie"}`
	arrHealth := `{"series":{"tvdbId":81189}}`
	stremioHealth := `{"imdb_id":"tt1234567","type":"movie"}`
	cases := []struct {
		name string
		rel  database.LibraryRelease
		want string
	}{
		{"stremio download", database.LibraryRelease{DownloadID: stremioDL}, "stremio"},
		{"stremio snapshot", database.LibraryRelease{Snapshot: database.ImportQueueItem{Metadata: &stremioMeta}}, "stremio"},
		{"arr snapshot", database.LibraryRelease{Snapshot: database.ImportQueueItem{Metadata: &arrMeta}}, "arrs"},
		{"arr health", database.LibraryRelease{HealthMetadata: &arrHealth}, "arrs"},
		{"stremio health", database.LibraryRelease{HealthMetadata: &stremioHealth}, "stremio"},
		{"manual empty", database.LibraryRelease{}, "manual"},
		{"manual junk", database.LibraryRelease{HealthMetadata: &[]string{"{}"}[0]}, "manual"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, releaseOrigin(&tc.rel))
		})
	}
}

func stremioOnlyTitle(mediaType, imdbID, name string, releases ...database.LibraryRelease) database.LibraryTitle {
	return database.LibraryTitle{Type: mediaType, IMDbID: imdbID, Name: name, Releases: releases}
}

func stremioOnlyRelease(origin string, season, episode int) database.LibraryRelease {
	return database.LibraryRelease{Origin: origin, Season: season, Episode: episode, Name: origin}
}

func TestLibraryStremioOnly(t *testing.T) {
	// Completion semantics: a Stremio anchor keeps ALL releases under the
	// title (ARR/manual files complete it). Real-IMDb titles without a
	// Stremio anchor stay visible too when they carry any release (the
	// collection owner's union rule: tt-keyed instant/ARR files join the
	// Stremio library); synthetic altm-* rows remain excluded until relinked.
	movie := stremioOnlyTitle("movie", "tt0133093", "The Matrix",
		stremioOnlyRelease("stremio", 0, 0),
		stremioOnlyRelease("arrs", 0, 0),
	)
	movieArrsOnly := stremioOnlyTitle("movie", "tt0111161", "Shawshank",
		stremioOnlyRelease("arrs", 0, 0),
	)
	episode := "tt0903747"
	epStremioOnly := stremioOnlyTitle("series", episode, "Breaking Bad",
		stremioOnlyRelease("stremio", 1, 1),
	)
	// One verified Stremio anchor keeps the whole show: overlapping ARR
	// episode, other episodes, and unverified releases all retained.
	epOverlap := stremioOnlyTitle("series", "tt0903748", "Better Call Saul",
		stremioOnlyRelease("stremio", 2, 3),
		stremioOnlyRelease("manual", 2, 3),
		stremioOnlyRelease("arrs", 2, 4),
		stremioOnlyRelease("stremio", 0, 0),
	)
	// Unverified-only Stremio release anchors nothing...
	epUnverified := stremioOnlyTitle("series", "tt0903749", "El Camino",
		stremioOnlyRelease("stremio", 0, 0),
	)
	// ...but the fallback keeps the tt-keyed title itself.
	// A synthetic title never appears until relinked to a tt key.
	epSynthetic := stremioOnlyTitle("series", "altm-series-breaking-bad", "Breaking Bad",
		stremioOnlyRelease("stremio", 1, 1),
	)
	// tt-only title with zero releases stays hidden (nothing to show).
	empty := stremioOnlyTitle("movie", "tt1375666", "Inception")
	out := LibraryStremioOnly([]database.LibraryTitle{movie, movieArrsOnly, epStremioOnly, epOverlap, epUnverified, epSynthetic, empty})
	require.Len(t, out, 5)
	byID := map[string]database.LibraryTitle{}
	for _, title := range out {
		byID[title.IMDbID] = title
	}
	// Anchored movie keeps both Stremio + ARR files.
	require.Contains(t, byID, "tt0133093")
	require.Len(t, byID["tt0133093"].Releases, 2)
	// ARR-only tt title stays visible with all instant files.
	require.Contains(t, byID, "tt0111161")
	require.Len(t, byID["tt0111161"].Releases, 1)
	// Anchored show keeps every release (order preserved).
	require.Contains(t, byID, "tt0903748")
	require.Len(t, byID["tt0903748"].Releases, 4)
	require.Equal(t, 2, byID["tt0903748"].Releases[0].Season)
	// Unverified tt show is kept by the fallback rule (not dropped).
	require.Contains(t, byID, "tt0903749")
	// Synthetic look-alikes and release-less tt titles never appear.
	require.NotContains(t, byID, "altm-series-breaking-bad")
	require.NotContains(t, byID, "tt1375666")
}

func TestCleanSearchQuery(t *testing.T) {
	require.Equal(t, "Presumed Innocent", cleanSearchQuery("148771-Presumed.Innocent.S01E02"))
	require.Equal(t, "Presumed Innocent", cleanSearchQuery("Presumed.Innocent"))
	require.Equal(t, "", cleanSearchQuery("12345"))
	require.Equal(t, "presumedinnocent", normalizeSearchTitle("Presumed Innocent!"))
}

func TestEnrichLibraryTitleBySearchSeries(t *testing.T) {
	var gotCatalog, gotMeta bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/catalog/") {
			gotCatalog = true
		}
		if strings.HasPrefix(r.URL.Path, "/meta/") {
			gotMeta = true
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/catalog/") {
			_, _ = w.Write([]byte(`{"metas":[{"id":"tt13403046","name":"Presumed Innocent","type":"series"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"meta":{"id":"tt13403046","type":"series","name":"Presumed Innocent","year":"2024","poster":"https://example.invalid/p.jpg","description":"Courtroom drama."}}`))
	}))
	defer srv.Close()
	previous := cinemetaBaseURL
	cinemetaBaseURL = srv.URL
	defer func() { cinemetaBaseURL = previous }()
	cinemetaSearchCache.reset()

	title := database.LibraryTitle{
		Type:     "series",
		IMDbID:   "altm-series-presumed-innocent",
		Name:     "148771-Presumed.Innocent",
		Metadata: json.RawMessage(`{"id":"altm-series-presumed-innocent","type":"series","name":"148771-Presumed.Innocent"}`),
	}
	enriched := enrichLibraryTitleBySearch(context.Background(), title, "test-agent")
	require.Equal(t, "tt13403046", enriched.IMDbID)
	require.Equal(t, "Presumed Innocent", enriched.Name)
	require.True(t, gotCatalog, "expected a series catalog search request")
	require.True(t, gotMeta, "expected a meta detail fetch request")
	require.Contains(t, string(enriched.Metadata), "Courtroom drama.")
}
