package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math/rand"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kipsilabs/altmount/internal/database"
	"github.com/kipsilabs/altmount/internal/stremio"
)

// libraryRepairResolverVersion bumps when identity evidence rules change so
// garbage suppressed under an older version becomes due again.
const libraryRepairResolverVersion = 2

// libraryRepairLeaseSeconds bounds one repair claim; expired leases are
// recovered by LeaseLibraryRepairDue (crash-safe). It must exceed the
// worst-case pass duration — 200 rows × 2s pacing gap ≈ 400s plus DB
// overhead — so a live claim never lapses underneath its own worker.
const libraryRepairLeaseSeconds = 900

// libraryRepairBatchCap bounds one scheduler pass; canonical and synthetic
// queues interleave fairly inside it.
const libraryRepairBatchCap = 200

// libraryRepairEnrichGap paces enriching calls against Cinemeta's public
// endpoint, which throttles bursts (an unpaced pass ran at ~1.7 req/s and
// lost ~75% of rows to rate limits). Two seconds keeps a full 200-row pass
// inside the 8-minute automatic pass budget while staying at ~0.5 req/s.
const libraryRepairEnrichGap = 2 * time.Second

// libraryRepairMaxAttempts caps retries before a title parks as terminal.
const libraryRepairMaxAttempts = 8

// identityEvidence is the compact, auditable input to identity resolution:
// never credentials, never full paths.
type identityEvidence struct {
	TitleName    string `json:"title_name"`
	MediaType    string `json:"media_type"`
	Year         int    `json:"year"`
	ParentFolder string `json:"parent_folder,omitempty"`
	NZBName      string `json:"nzb_name,omitempty"`
	Source       string `json:"source"`
}

func fingerprintIdentity(ev identityEvidence) string {
	raw, _ := json.Marshal(ev)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// libraryTitleCanonicalYear extracts the title's canonical start year from
// its metadata (releaseInfo first — it may be a range like "2019-2027", the
// start year is still canonical — then the plain year field). 0 when neither
// is a plain start year.
func libraryTitleCanonicalYear(t database.LibraryTitle) int {
	var meta struct {
		ReleaseInfo string `json:"releaseInfo"`
		Year        string `json:"year"`
	}
	if json.Unmarshal(t.Metadata, &meta) != nil {
		return 0
	}
	for _, y := range []string{meta.ReleaseInfo, meta.Year} {
		if y = strings.TrimSpace(y); y != "" {
			if n, err := strconv.Atoi(y); err == nil && n > 1800 && n < 2150 {
				return n
			}
		}
	}
	return 0
}

// libraryTitleBaseName strips a parenthesized/bracketed year from a title
// name ("Africa (2001)" -> "Africa") for synthetic split anchors.
var libraryYearParenPattern = regexp.MustCompile(`\s*[\(\[](19|20)\d{2}[\)\]]\s*`)

func libraryTitleBaseName(name string) string {
	base := strings.TrimSpace(libraryYearParenPattern.ReplaceAllString(name, " "))
	base = strings.Join(strings.Fields(base), " ")
	return base
}

// libraryReleaseParsedYear reports the release's own year: primarily from
// the shared release parser (scene name), falling back to a parenthesized
// year in the release's parent folder or name (ARR layouts like "tv/Africa
// (2001)/Season 01"). The sanity range rejects resolution tags like 2160p.
// 0 when no confident year is found — the split audit skips year-less
// releases instead of guessing.
func libraryReleaseParsedYear(r database.LibraryRelease) int {
	if parsed := stremio.ParseReleaseTitle(r.Name); parsed.Year > 1800 && parsed.Year < 2150 {
		return parsed.Year
	}
	if m := libraryYearExtractPattern.FindString(filepath.Base(filepath.Dir(r.FilePath))); m != "" {
		return libraryYearInRange(m)
	}
	if m := libraryYearParenPattern.FindString(r.Name); m != "" {
		return libraryYearInRange(m)
	}
	return 0
}

// libraryYearExtractPattern finds a year-shaped four digit value; callers
// keep its use scoped to folder names and parenthesized fragments so
// quality tags cannot dominate.
var libraryYearExtractPattern = regexp.MustCompile(`(?:19|20)\d{2}`)

func libraryYearInRange(s string) int {
	y, err := strconv.Atoi(s)
	if err != nil || y <= 1800 || y >= 2150 {
		return 0
	}
	return y
}

// genericFolderPattern rejects folders that carry no title info.
var genericFolderPattern = func() func(string) bool {
	generic := map[string]struct{}{
		"movies": {}, "movie": {}, "tv": {}, "tvshows": {}, "tv-shows": {}, "series": {},
		"complete": {}, "downloads": {}, "usenet": {}, "nzb": {}, "nzbs": {},
		"audio": {}, "music": {}, "books": {}, "ebooks": {}, "games": {},
	}
	return func(name string) bool {
		_, ok := generic[strings.ToLower(strings.TrimSpace(name))]
		return ok
	}
}()

// extractIdentityEvidence unifies identity extraction across autosave, health
// import, and repair. Precedence:
//  1. Validated Stremio IMDb ID (never inferred).
//  2. Trusted ARR provider IDs (tmdb/tvdb) through verified mapping.
//  3. Parent-folder walk for obfuscated basenames (LibraryPath first).
//  4. Cleaned title tokens for search.
//
// Returns ok=false when nothing is usable (hash-only, segment, generic) —
// callers park for review instead of burning provider calls.
func extractIdentityEvidence(title database.LibraryTitle, release database.LibraryRelease) (ev identityEvidence, query string, ok bool) {
	ev.MediaType = title.Type
	ev.TitleName = strings.TrimSpace(title.Name)
	ev.Source = release.Source
	if ev.Source == "" {
		ev.Source = "queue"
	}
	year := 0
	var meta struct {
		ReleaseInfo string `json:"releaseInfo"`
		Year        string `json:"year"`
	}
	if json.Unmarshal(title.Metadata, &meta) == nil {
		for _, y := range []string{meta.ReleaseInfo, meta.Year} {
			if y = strings.TrimSpace(y); y != "" {
				if n, err := strconv.Atoi(y); err == nil && n > 1800 && n < 2150 {
					year = n
					break
				}
			}
		}
	}
	ev.Year = year

	// Obfuscated title with usable parent evidence: prefer the parent folder
	// (LibraryPath/file path), else the NZB name.
	base := ev.TitleName
	if isObfuscatedName(base) || segmentNamePattern.MatchString(base) {
		parent := ""
		for _, p := range []string{release.FilePath, release.NzbPath} {
			if strings.TrimSpace(p) == "" {
				continue
			}
			dir := filepath.Dir(p)
			for i := 0; i < 3 && dir != "." && dir != "/" && dir != ""; i++ {
				candidate := filepath.Base(dir)
				if !isObfuscatedName(candidate) && !segmentNamePattern.MatchString(candidate) &&
					len(candidate) > 3 && !genericFolderPattern(candidate) {
					parent = candidate
					break
				}
				dir = filepath.Dir(dir)
			}
			if parent != "" {
				break
			}
		}
		if nzb := filepath.Base(strings.TrimSpace(release.NzbPath)); parent == "" && nzb != "" {
			if trimmed := strings.TrimSuffix(nzb, filepath.Ext(nzb)); !isObfuscatedName(trimmed) && len(trimmed) > 3 {
				ev.NZBName = trimmed
			}
		}
		if parent == "" && ev.NZBName == "" {
			return ev, "", false
		}
		ev.ParentFolder = parent
		queryBase := parent
		if queryBase == "" {
			queryBase = ev.NZBName
		}
		q := cleanSearchQuery(queryBase)
		if q == "" || normalizeSearchTitle(q) == "" {
			return ev, "", false
		}
		return ev, q, true
	}

	// Clean title: usable directly when it survives cleaning.
	q := cleanSearchQuery(base)
	if q == "" || normalizeSearchTitle(q) == "" {
		return ev, "", false
	}
	return ev, q, true
}

// repairOutcome classifies one repair attempt for ledger + counters.
type repairOutcome struct {
	code        string // reason code for ledger
	terminal    bool
	needsReview bool
	backoff     time.Duration
}

// classifyRepairOutcome maps attempt results to retry policy:
// transport/429/5xx → backoff; 404/mismatch → long cooldown; garbage → review.
func classifyRepairOutcome(transient bool, notFound bool, mismatch bool, emptyQuery bool, ambiguous bool) repairOutcome {
	switch {
	case emptyQuery:
		return repairOutcome{code: "empty_query", needsReview: true}
	case ambiguous:
		return repairOutcome{code: "search_ambiguous", needsReview: true}
	case mismatch:
		return repairOutcome{code: "enrich_mismatch", backoff: 24 * time.Hour}
	case notFound:
		return repairOutcome{code: "enrich_not_found", backoff: 72 * time.Hour}
	case transient:
		return repairOutcome{code: "enrich_transient", backoff: 15 * time.Minute}
	default:
		return repairOutcome{code: "search_miss", backoff: 12 * time.Hour}
	}
}

// repairBackoff adds jitter and caps growth across attempts.
func repairBackoff(base time.Duration, attempts int) time.Duration {
	if base <= 0 {
		base = time.Minute
	}
	d := base
	for i := 0; i < attempts && d < 72*time.Hour; i++ {
		d *= 2
	}
	if d > 72*time.Hour {
		d = 72 * time.Hour
	}
	jitter := time.Duration(rand.Int63n(int64(d)/4 + 1))
	return d + jitter
}

// libraryRepairCounters aggregates one scheduler pass for structured logging.
type libraryRepairCounters struct {
	attempted int
	resolved  int
	relinked  int
	posterAdd int
	transient int
	ambiguous int
	rejected  int
	exhausted int
	review    int
	deferred  int
	split     int
}

func (c libraryRepairCounters) log(ctx context.Context) {
	slog.InfoContext(ctx, "Library repair pass complete",
		"attempted", c.attempted, "resolved", c.resolved, "relinked", c.relinked,
		"posters", c.posterAdd, "transient", c.transient, "ambiguous", c.ambiguous,
		"rejected", c.rejected, "exhausted", c.exhausted, "review", c.review,
		"deferred", c.deferred, "split", c.split)
}
