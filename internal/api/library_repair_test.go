package api

import (
	"testing"
	"time"

	"github.com/kipsilabs/altmount/internal/database"
	"github.com/stretchr/testify/require"
)

func TestExtractIdentityEvidenceObfuscatedParent(t *testing.T) {
	// 1917 case: hash title, informative parent folder in file path.
	title := database.LibraryTitle{Type: "movie", IMDbID: "altm-movie-058c00564b", Name: "058c00564b21470bb99e042ac8b1d0b6"}
	release := database.LibraryRelease{
		Source:   "health",
		FilePath: "complete/Movies/1917.2019.UHD.BluRay.2160p.TrueHD.Atmos.7.1.HEVC.REMUX-FraMeSToR/058c00564b21470bb99e042ac8b1d0b6.mkv",
		NzbPath:  "/config/.nzbs/Movies/132222-1917.2019.UHD.BluRay.2160p.TrueHD.Atmos.7.1.HEVC.REMUX-FraMeSToR.nzbz",
	}
	ev, query, ok := extractIdentityEvidence(title, release)
	require.True(t, ok)
	// "1917" is consumed as the year by the release parser, so the search
	// query keeps the edition tokens; the year travels in evidence.
	require.NotEmpty(t, query)
	require.NotEmpty(t, ev.ParentFolder)
	require.Contains(t, ev.ParentFolder, "1917")

	// Hash with no informative parent or NZB: review, no query.
	bare := database.LibraryTitle{Type: "movie", IMDbID: "altm-movie-adc47093", Name: "adc4709367c04951b8aaf123d57cd3c6"}
	_, query, ok = extractIdentityEvidence(bare, database.LibraryRelease{})
	require.False(t, ok)
	require.Empty(t, query)

	// Clean title passes through directly.
	clean := database.LibraryTitle{Type: "movie", IMDbID: "altm-movie-foo", Name: "Whiplash.2014.PROPER.BluRay.1080p"}
	_, query, ok = extractIdentityEvidence(clean, database.LibraryRelease{})
	require.True(t, ok)
	require.Contains(t, query, "Whiplash")

	// Generic folder parents are rejected.
	gen := database.LibraryTitle{Type: "movie", IMDbID: "altm-movie-x", Name: "058c00564b21470bb99e042ac8b1d0b6"}
	_, _, ok = extractIdentityEvidence(gen, database.LibraryRelease{FilePath: "complete/Movies/058c00564b21470bb99e042ac8b1d0b6.mkv"})
	require.False(t, ok)
}

func TestClassifyRepairOutcome(t *testing.T) {
	require.Equal(t, "empty_query", classifyRepairOutcome(false, false, false, true, false).code)
	require.True(t, classifyRepairOutcome(false, false, false, true, false).needsReview)
	require.Equal(t, "search_ambiguous", classifyRepairOutcome(false, false, false, false, true).code)
	require.Equal(t, "enrich_mismatch", classifyRepairOutcome(false, false, true, false, false).code)
	require.Equal(t, "enrich_not_found", classifyRepairOutcome(false, true, false, false, false).code)
	require.Equal(t, "enrich_transient", classifyRepairOutcome(true, false, false, false, false).code)
}

func TestRepairBackoffCaps(t *testing.T) {
	require.Greater(t, int64(repairBackoff(0, 0)), int64(0))
	grown := repairBackoff(15*time.Minute, 10)
	require.Greater(t, int64(grown), int64(0))
	require.LessOrEqual(t, int64(grown), int64(90*time.Hour))
}
