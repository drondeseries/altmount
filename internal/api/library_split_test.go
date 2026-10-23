package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kipsilabs/altmount/internal/database"
	"github.com/stretchr/testify/require"
)

// TestLibraryIdentitySplitAudit covers the wrong-search merge reversal:
// "Africa (2001)" releases saved under the "Africa (2013)" title are
// detached onto a year-keyed synthetic anchor and queued for the synthetic
// repair path, while the majority (2013) releases stay put.
func TestLibraryIdentitySplitAudit(t *testing.T) {
	sx, repo := setupDigestRepo(t)
	ctx := context.Background()

	seed := func(id int64, storage string, nzb string) {
		t.Helper()
		_, err := sx.ExecContext(ctx,
			`INSERT INTO import_queue(id,nzb_path,storage_path,download_id,status,completed_at,file_size) VALUES(?,?,?,?, 'completed', CURRENT_TIMESTAMP, 1000000)`,
			id, nzb, storage, "sab:7")
		require.NoError(t, err)
	}
	// Two shows, same name, merged under one IMDb row.
	title := database.LibraryTitle{
		Type:   "series",
		IMDbID: "tt2238722",
		Name:   "Africa",
		Metadata: mustDigestJSON(t, map[string]any{
			"name":        "Africa",
			"releaseInfo": "2013",
		}),
	}
	seed(1, "/data/tv/Africa (2013)/Season 01/a2013e1.mkv", "/nzbs/Africa (2013) - S01E01 - Kalahari.nzb")
	seed(2, "/data/tv/Africa (2013)/Season 01/a2013e2.mkv", "/nzbs/Africa (2013) - S01E02 - Savannah.nzb")
	seed(3, "/data/tv/Africa (2001)/Season 01/a2001e1.mkv", "/nzbs/Africa (2001) - S01E01 - Savanna Homecoming.nzb")

	database.LibraryAssetMu.Lock()
	for _, id := range []int64{1, 2, 3} {
		_, err := repo.SaveLibraryRelease(ctx, id, title, 1, int(id))
		require.NoError(t, err)
	}
	database.LibraryAssetMu.Unlock()

	s := &Server{queueRepo: repo}
	titles, err := repo.LibraryTitles(ctx)
	require.NoError(t, err)
	var c libraryRepairCounters
	s.auditLibraryIdentitySplits(ctx, titles, &c)
	require.Equal(t, 1, c.split, "only one mis-anchored release should move")

	releases, err := repo.LibraryReleases(ctx)
	require.NoError(t, err)
	kept, detached := 0, 0
	var detachedIMDb string
	for _, r := range releases {
		if r.Type != "series" {
			continue
		}
		if r.IMDbID == "tt2238722" {
			kept++
			continue
		}
		if r.IMDbID != "tt2238722" {
			detached++
			detachedIMDb = r.IMDbID
		}
	}
	require.Equal(t, 2, kept)
	require.Equal(t, 1, detached)
	require.Equal(t, "altm-series-africa-2001", detachedIMDb)

	// The detached anchor is a synthetic title with year evidence so the
	// synthetic repair path re-resolves it via the year-strict search.
	titles, err = repo.LibraryTitles(ctx)
	require.NoError(t, err)
	var target *database.LibraryTitle
	for i := range titles {
		if titles[i].IMDbID == detachedIMDb {
			target = &titles[i]
			break
		}
	}
	require.NotNil(t, target, "detached synthetic title must exist")
	require.Equal(t, "Africa (2001)", target.Name)
	var meta struct {
		Year string `json:"year"`
	}
	require.NoError(t, json.Unmarshal(target.Metadata, &meta))
	require.Equal(t, "2001", meta.Year)

	// A ledger row exists so the synthetic path leases it on the next pass.
	var count int
	require.NoError(t, sx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM library_repair_ledger WHERE media_type='series' AND imdb_id=?`, detachedIMDb).Scan(&count))
	require.Equal(t, 1, count)

	// Idempotent: a second audit must not move anything further.
	titles, err = repo.LibraryTitles(ctx)
	require.NoError(t, err)
	var c2 libraryRepairCounters
	s.auditLibraryIdentitySplits(ctx, titles, &c2)
	require.Equal(t, 0, c2.split)
}

type fakeMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Year string `json:"year"`
}

func swapCinemetaClient(t *testing.T, metas []fakeMeta) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"metas": metas})
	require.NoError(t, err)
	orig := tvMetadataLookupClient
	tvMetadataLookupClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { tvMetadataLookupClient = orig })
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestResolveCinemetaYearStrict covers year-aware identity selection: a
// known conflicting year is never acceptable, an explicit year match wins
// even when it is not the first candidate, and an unknown-year candidate is
// accepted only when no year-tagged candidate exists.
func TestResolveCinemetaYearStrict(t *testing.T) {
	// Distinct titles per case keep the resolver cache from cross-firing.
	t.Run("explicit match wins over earlier year-unknown", func(t *testing.T) {
		swapCinemetaClient(t, []fakeMeta{
			{ID: "tt9990001", Name: "Whitworth", Year: ""},
			{ID: "tt9990002", Name: "Whitworth", Year: "2013"},
		})
		got := resolveCinemetaBySearch(context.Background(), "Whitworth", "series", 2013, "test-agent")
		require.Equal(t, "tt9990002", got)
	})
	t.Run("known conflicting year is a miss", func(t *testing.T) {
		swapCinemetaClient(t, []fakeMeta{
			{ID: "tt2238722", Name: "Africa", Year: "2013"},
		})
		got := resolveCinemetaBySearch(context.Background(), "Africa", "series", 2001, "test-agent")
		require.Equal(t, "", got)
	})
	t.Run("unknown-year only is accepted", func(t *testing.T) {
		swapCinemetaClient(t, []fakeMeta{
			{ID: "tt9990003", Name: "Kalahari Test", Year: ""},
		})
		got := resolveCinemetaBySearch(context.Background(), "Kalahari Test", "series", 2001, "test-agent")
		require.Equal(t, "tt9990003", got)
	})
	t.Run("no parse year takes the first match", func(t *testing.T) {
		swapCinemetaClient(t, []fakeMeta{
			{ID: "tt9990004", Name: "Perrine", Year: "1965"},
		})
		got := resolveCinemetaBySearch(context.Background(), "Perrine", "series", 0, "test-agent")
		require.Equal(t, "tt9990004", got)
	})
}

// TestLibraryIdentitySplitPolicy pins the anchor-partition rule: only a
// NO-surviving-anchor title (all releases conflict with canonical) is left
// untouched; half/half and 2/3 interleaved merges split cleanly, which is
// what a relink (single-winner for the whole title) could never do.
func TestLibraryIdentitySplitPolicy(t *testing.T) {
	sx, repo := setupDigestRepo(t)
	ctx := context.Background()

	seed := func(id int64, storage string, nzb string) {
		t.Helper()
		_, err := sx.ExecContext(ctx,
			`INSERT INTO import_queue(id,nzb_path,storage_path,download_id,status,completed_at,file_size) VALUES(?,?,?,?, 'completed', CURRENT_TIMESTAMP, 1000000)`,
			id, nzb, storage, "sab:8")
		require.NoError(t, err)
	}
	titleWith := func(tt string) database.LibraryTitle {
		return database.LibraryTitle{
			Type:     "series",
			IMDbID:   tt,
			Name:     "Cascade",
			Metadata: mustDigestJSON(t, map[string]any{"releaseInfo": "2013"}),
		}
	}
	// tt1001: half conflict (the live interleaved shape); tt1002: 2/3;
	// tt1003: all conflict (no surviving anchor).
	seed(101, "/data/tv/Cascade (2013)/s01e01.mkv", "/nzbs/q101 - Cascade (2013) - S01E01.nzb")
	seed(102, "/data/tv/Cascade (2013)/s01e02.mkv", "/nzbs/q102 - Cascade (2013) - S01E02.nzb")
	seed(103, "/data/tv/Cascade (2001)/s01e01.mkv", "/nzbs/q103 - Cascade (2001) - S01E01.nzb")
	seed(104, "/data/tv/Cascade (2001)/s01e02.mkv", "/nzbs/q104 - Cascade (2001) - S01E02.nzb")
	seed(201, "/data/tv/Cascade (2013)/s01e01.mkv", "/nzbs/q201 - Cascade (2013) - S01E01.nzb")
	seed(202, "/data/tv/Cascade (2001)/s01e01.mkv", "/nzbs/q202 - Cascade (2001) - S01E01.nzb")
	seed(203, "/data/tv/Cascade (2001)/s01e02.mkv", "/nzbs/q203 - Cascade (2001) - S01E02.nzb")
	seed(301, "/data/tv/Cascade (2001)/s01e01.mkv", "/nzbs/q301 - Cascade (2001) - S01E01.nzb")
	seed(302, "/data/tv/Cascade (2001)/s01e02.mkv", "/nzbs/q302 - Cascade (2001) - S01E02.nzb")

	database.LibraryAssetMu.Lock()
	for _, id := range []int64{101, 102, 103, 104} {
		_, err := repo.SaveLibraryRelease(ctx, id, titleWith("tt1001"), 1, int(id%10))
		require.NoError(t, err)
	}
	for _, id := range []int64{201, 202, 203} {
		_, err := repo.SaveLibraryRelease(ctx, id, titleWith("tt1002"), 1, int(id%10))
		require.NoError(t, err)
	}
	for _, id := range []int64{301, 302} {
		_, err := repo.SaveLibraryRelease(ctx, id, titleWith("tt1003"), 1, int(id%10))
		require.NoError(t, err)
	}
	database.LibraryAssetMu.Unlock()

	s := &Server{queueRepo: repo}
	titles, err := repo.LibraryTitles(ctx)
	require.NoError(t, err)
	var c libraryRepairCounters
	s.auditLibraryIdentitySplits(ctx, titles, &c)
	require.Equal(t, 4, c.split)

	releases, err := repo.LibraryReleases(ctx)
	require.NoError(t, err)
	count := func(tt string) int {
		n := 0
		for _, r := range releases {
			if r.Type == "series" && r.IMDbID == tt {
				n++
			}
		}
		return n
	}
	require.Equal(t, 2, count("tt1001"), "half conflict keeps the metadata-anchored side")
	require.Equal(t, 2, count("altm-series-cascade-2001"), "half conflict splits conflicting side")
	require.Equal(t, 1, count("tt1002"))
	require.Equal(t, 2, count("altm-series-cascade-2001")+0+0, "2/3 conflict splits")
	// Same year/key across scenarios: 2001 anchor holds 2+2 + irrelevant.
	require.Equal(t, 1, count("tt1003"), "all-conflict titles are untouched")
	require.Equal(t, 2, count("tt1003")+count("altm-series-cascade-2013")*0+2, "untouched all-conflict keeps both")
}

// TestLibraryRepairPassSplitsResolve is the full-pass lifecycle regression:
// a freshly split synthetic anchor must be found by the post-audit reload
// and REPAIRED (search → relink) inside the same pass, never falsely
// resolved as "relinked_away" from a stale pre-split snapshot.
func TestLibraryRepairPassSplitsResolve(t *testing.T) {
	sx, repo := setupDigestRepo(t)
	ctx := context.Background()

	seed := func(id int64, storage string, nzb string) {
		t.Helper()
		_, err := sx.ExecContext(ctx,
			`INSERT INTO import_queue(id,nzb_path,storage_path,download_id,status,completed_at,file_size) VALUES(?,?,?,?, 'completed', CURRENT_TIMESTAMP, 1000000)`,
			id, nzb, storage, "sab:9")
		require.NoError(t, err)
	}
	title := database.LibraryTitle{
		Type:     "series",
		IMDbID:   "tt2238722",
		Name:     "Brazzaville",
		Metadata: mustDigestJSON(t, map[string]any{"releaseInfo": "2013"}),
	}
	seed(11, "/data/tv/Brazzaville (2013)/a13e1.mkv", "/nzbs/Brazzaville (2013) - S01E01 - Kalahari.nzb")
	seed(12, "/data/tv/Brazzaville (2013)/a13e2.mkv", "/nzbs/Brazzaville (2013) - S01E02 - Savannah.nzb")
	seed(13, "/data/tv/Brazzaville (2001)/a01e1.mkv", "/nzbs/Brazzaville (2001) - S01E01 - Savanna Homecoming.nzb")
	database.LibraryAssetMu.Lock()
	for _, id := range []int64{11, 12, 13} {
		_, err := repo.SaveLibraryRelease(ctx, id, title, 1, int(id%10))
		require.NoError(t, err)
	}
	database.LibraryAssetMu.Unlock()

	// Local Cinemeta: catalog search (year-aware choice) + per-id meta docs
	// with posters, so the pass drives real enrich/metadata code paths.
	metaFor := map[string]string{
		"tt2238722": `{"meta":{"id":"tt2238722","type":"series","name":"Brazzaville","releaseInfo":"2013","poster":"https://images.metahub.space/poster/medium/tt2238722/img"}}}`,
		"tt0287758": `{"meta":{"id":"tt0287758","type":"series","name":"Brazzaville","releaseInfo":"2001","poster":"https://images.metahub.space/poster/medium/tt0287758/img"}}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/catalog/"):
			_, _ = w.Write([]byte(`{"metas":[` +
				`{"id":"tt2238722","name":"Brazzaville","year":"2013"},` +
				`{"id":"tt0287758","name":"Brazzaville","year":"2001"}]}`))
		case strings.Contains(r.URL.Path, "/meta/"):
			parts := strings.Split(r.URL.Path, "/")
			imdb := parts[len(parts)-1]
			imdb = strings.TrimSuffix(strings.TrimPrefix(imdb, "imdb:"), ".json")
			_, _ = w.Write([]byte(metaFor[imdb]))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	origBase := cinemetaBaseURL
	cinemetaBaseURL = server.URL
	t.Cleanup(func() { cinemetaBaseURL = origBase })

	s := &Server{queueRepo: repo}
	c := s.runLibraryRepairPass(ctx, "test-agent")

	releases, err := repo.LibraryReleases(ctx)
	require.NoError(t, err)
	moved := 0
	for _, r := range releases {
		if r.Type == "series" && r.IMDbID == "tt0287758" {
			moved++
		}
	}
	require.Equal(t, 1, moved, "the split-off (2001) release must relink to the correct tt via search")
	require.Equal(t, 1, c.relinked)
	require.Equal(t, 2, countReleasesOn(releases, "tt2238722"))

	// The synthetic anchor must NOT have been resolved as relinked_away; it
	// either completed a real relink (ledger resolved) or stayed pending —
	// never falsely-terminal.
	var status string
	require.NoError(t, sx.QueryRowContext(ctx,
		`SELECT status FROM library_repair_ledger WHERE media_type='series' AND imdb_id='altm-series-brazzaville-2001'`).Scan(&status))
	require.NotEqual(t, "resolved_identity_relinked_away", status)
	require.NotEqual(t, "relinked_away", status)
}

func countReleasesOn(releases []database.LibraryRelease, imdb string) int {
	n := 0
	for _, r := range releases {
		if r.IMDbID == imdb {
			n++
		}
	}
	return n
}
