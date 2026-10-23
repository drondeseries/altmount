package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kipsilabs/altmount/internal/database"
	"github.com/kipsilabs/altmount/internal/stremio"
)

// Library item sources for auto-save classification.
const (
	librarySourceStremio = "stremio"
	librarySourceArrs    = "arrs"
	librarySourceOther   = "other"
)

// Cinemeta search lookup: resolves a parsed title+year to an IMDb ID for
// releases without one (manual uploads, ARR items stripped of IDs). Results
// are cached per (title,year) pair; misses are remembered for 24h.
var cinemetaSearchCache seriesMetadataCache[string]

// cinemetaBaseURL is the Cinemeta API root. A variable so tests can point
// the resolvers at a local server instead of the public API.
var cinemetaBaseURL = "https://v3-cinemeta.strem.io"

// normalizeSearchTitle reduces a title to lowercase alphanumerics so
// "Presumed.Innocent", "Presumed Innocent!" and "presumedinnocent" compare
// equal despite scene punctuation.
func normalizeSearchTitle(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var leadingIndexerIDPattern = regexp.MustCompile(`^\d+[-. _]+`)

var episodeTailPattern = regexp.MustCompile(`(?i)[. _-]*s\d{1,2}e\d{1,3}.*$`)

// cleanSearchQuery strips indexer-ID prefixes ("148771-Presumed.Innocent")
// and scene separators so the query resembles a real title.
func cleanSearchQuery(title string) string {
	q := leadingIndexerIDPattern.ReplaceAllString(strings.TrimSpace(title), "")
	q = episodeTailPattern.ReplaceAllString(q, "")
	q = strings.Join(strings.Fields(q), " ")
	if q != "" {
		allDigits := true
		for _, r := range q {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return ""
		}
	}
	q = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(q, ".", " "), "_", " "), "-", " ")
	return strings.Join(strings.Fields(q), " ")
}

// resolveCinemetaBySearch searches Cinemeta for a title+year match and returns
// the best IMDb ID ("" when nothing confident found). Year must match when
// both sides know it, to avoid picking the wrong film.
func resolveCinemetaBySearch(ctx context.Context, title, mediaType string, year int, userAgent string) string {
	clean := cleanSearchQuery(title)
	if clean == "" {
		return ""
	}
	want := normalizeSearchTitle(clean)
	if want == "" {
		return ""
	}
	key := mediaType + "|" + want + "|" + strconv.Itoa(year)
	if cached, ok := cinemetaSearchCache.get(key); ok {
		return cached
	}
	miss := ""
	searchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	catalog := "movie"
	if mediaType == "series" {
		catalog = "series"
	}
	u := cinemetaBaseURL + "/catalog/" + catalog + "/top/search=" + url.QueryEscape(clean) + ".json"
	req, err := http.NewRequestWithContext(searchCtx, http.MethodGet, u, nil)
	if err != nil {
		return miss
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := tvMetadataLookupClient.Do(req)
	if err != nil {
		return miss
	}
	defer resp.Body.Close()
	// Only 404 means "no such title" worth remembering; rate limits and
	// server errors stay retryable so bulk imports survive throttling.
	if resp.StatusCode == http.StatusNotFound {
		cinemetaSearchCache.put(key, miss)
		return miss
	}
	if resp.StatusCode != http.StatusOK {
		return miss
	}
	var results struct {
		Metas []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Year string `json:"year"`
		} `json:"metas"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&results); err != nil {
		cinemetaSearchCache.put(key, miss)
		return miss
	}
	wantYear := ""
	if year > 0 {
		wantYear = strconv.Itoa(year)
	}
	// Year-aware selection among exact-name matches:
	//   - no parse year: first match wins (previous behavior).
	//   - parse year known: candidates whose Cinemeta year conflicts are never
	//     acceptable — two distinct releases (e.g. "Africa (2001)" and
	//     "Africa (2013)") must not collapse onto one IMDb ID just because the
	//     first candidate happened to come first.
	//   - a candidate with an unknown year is accepted only when it is the only
	//     surviving evidence, i.e. no year-tagged candidate was seen at all.
	firstYearUnknown := ""
	sawKnownYear := false
	for _, m := range results.Metas {
		if !libraryIMDbPattern.MatchString(m.ID) {
			continue
		}
		if normalizeSearchTitle(m.Name) != want {
			continue
		}
		if wantYear == "" {
			cinemetaSearchCache.put(key, m.ID)
			return m.ID
		}
		if m.Year == "" {
			if firstYearUnknown == "" {
				firstYearUnknown = m.ID
			}
			continue
		}
		sawKnownYear = true
		// Cinemeta years may be ranges ("2001", "2001-2004"); prefix match on
		// the start year keeps both forms.
		if strings.HasPrefix(m.Year, wantYear) {
			cinemetaSearchCache.put(key, m.ID)
			return m.ID
		}
	}
	if !sawKnownYear && firstYearUnknown != "" {
		cinemetaSearchCache.put(key, firstYearUnknown)
		return firstYearUnknown
	}
	cinemetaSearchCache.put(key, miss)
	return miss
}

// enrichLibraryTitleBySearch resolves a title without an IMDb ID via Cinemeta
// search and replaces the title identity when a confident match is found.
func enrichLibraryTitleBySearch(ctx context.Context, t database.LibraryTitle, userAgent string) database.LibraryTitle {
	if strings.HasPrefix(t.IMDbID, "tt") {
		return t
	}
	// Parse the release name first: the title proper sits before any
	// SxxExx marker, year, or quality tags ("1917.2019.UHD..." -> "1917").
	queryBase := t.Name
	year := 0
	if parsed := stremio.ParseReleaseTitle(t.Name); strings.TrimSpace(parsed.SeriesTitle) != "" {
		queryBase = parsed.SeriesTitle
		if parsed.Year > 0 {
			year = parsed.Year
		}
	}
	if year == 0 {
		var meta struct {
			ReleaseInfo string `json:"releaseInfo"`
			Year        string `json:"year"`
		}
		if json.Unmarshal(t.Metadata, &meta) == nil {
			for _, y := range []string{meta.ReleaseInfo, meta.Year} {
				if y != "" {
					if n, err := strconv.Atoi(y); err == nil {
						year = n
						break
					}
				}
			}
		}
	}
	imdbID := resolveCinemetaBySearch(ctx, queryBase, t.Type, year, userAgent)
	if imdbID == "" {
		return t
	}
	resolved := database.LibraryTitle{Type: t.Type, IMDbID: imdbID, Name: t.Name, Metadata: t.Metadata}
	return enrichLibraryTitle(ctx, resolved)
}

// librarySyntheticIDPrefix marks titles without an IMDb ID (ARR and generic
// imports). Synthetic IDs contain no colons so series episode suffixes
// ("<id>:season:episode") still parse unambiguously.
const librarySyntheticIDPrefix = "altm-"

var libraryEpisodePattern = regexp.MustCompile(`(?i)[. _-]s(\d{1,2})e(\d{1,3})(?:[. _-]|$)`)
var librarySlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// classifyLibraryItem reports where a completed queue item came from:
// Stremio plays (stremio: download ID), ARR post-processing (SABnzbd
// series_title/movie_title metadata), or anything else.
func classifyLibraryItem(item *database.ImportQueueItem) string {
	if item == nil {
		return librarySourceOther
	}
	if item.DownloadID != nil && strings.HasPrefix(*item.DownloadID, stremioDownloadIDPrefix) {
		return librarySourceStremio
	}
	if title, _ := arrTitleFromMetadata(item.Metadata); title != "" {
		return librarySourceArrs
	}
	return librarySourceOther
}

// libraryAutoSaveScopeAllows reports whether an auto-save scope covers a
// source. Scopes are distinct (not cumulative): "stremio" covers Stremio
// plays only, "arrs" covers ARR post-processing imports only, "all" covers
// every completed import with storage.
func libraryAutoSaveScopeAllows(scope, source string) bool {
	switch scope {
	case "stremio":
		return source == librarySourceStremio
	case "arrs":
		return source == librarySourceArrs
	case "all":
		return true
	default:
		return false
	}
}

// arrTitleFromMetadata extracts the ARR post-processing title (series or
// movie) from SABnzbd-style queue metadata. The second return is "series"
// or "movie" (series wins when both are present).
func arrTitleFromMetadata(raw *string) (string, string) {
	if raw == nil || *raw == "" {
		return "", ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*raw), &m); err != nil {
		return "", ""
	}
	if title := strings.TrimSpace(m["series_title"]); title != "" {
		return title, "series"
	}
	if title := strings.TrimSpace(m["movie_title"]); title != "" {
		return title, "movie"
	}
	return "", ""
}

// librarySlug normalizes a title into a stable synthetic-ID fragment.
func librarySlug(name string) string {
	slug := librarySlugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		slug = "untitled"
	}
	return slug
}

// libraryTitleForAutosave builds a library title for any identifiable
// completed import: Stremio items by IMDb ID, ARR items by post-processing
// title, and anything else by release-name heuristic (SxxExx → series,
// otherwise movie). Season/episode are populated when known.
//
// Identity evidence: when the NZB filename is obfuscated (hash-like) or a
// disc segment, the parent folder usually carries the real release name
// ("1917.2019.UHD...REMUX-FraMeSToR/058c0056....mkv"). The title is derived
// from that folder so search fallback can resolve it; the release keeps its
// own display name separately.
func libraryTitleForAutosave(item *database.ImportQueueItem) (database.LibraryTitle, int, int, bool) {
	if t, m, ok := libraryTitleForItem(item); ok {
		return t, m.Season, m.Episode, true
	}
	name := strings.TrimSuffix(filepath.Base(item.NzbPath), ".nzb")
	mediaType := "movie"
	if title, kind := arrTitleFromMetadata(item.Metadata); title != "" {
		name = title
		mediaType = kind
	} else if name == "" || name == "." || name == "/" {
		return database.LibraryTitle{}, 0, 0, false
	} else if m := libraryEpisodePattern.FindStringSubmatch(name); m != nil {
		mediaType = "series"
	}
	if isObfuscatedName(name) || segmentNamePattern.MatchString(name) {
		// Obfuscated/segment NZB filename: walk parents of the NZB path for
		// the real release folder ("<hash>.nzb" inside "1917.2019.../").
		dir := filepath.Dir(strings.TrimSpace(item.NzbPath))
		for i := 0; i < 3 && dir != "." && dir != "/" && dir != ""; i++ {
			candidate := filepath.Base(dir)
			if !isObfuscatedName(candidate) && !segmentNamePattern.MatchString(candidate) &&
				len(candidate) > 3 && !genericFolderPattern(candidate) {
				name = strings.TrimSuffix(candidate, ".nzb")
				break
			}
			dir = filepath.Dir(dir)
		}
	}
	id := librarySyntheticIDPrefix + mediaType + "-" + librarySlug(name)
	meta := fiber.Map{"id": id, "type": mediaType, "name": name}
	season, episode := 0, 0
	if mediaType == "series" {
		if m := libraryEpisodePattern.FindStringSubmatch(name); m != nil {
			season, _ = strconv.Atoi(m[1])
			episode, _ = strconv.Atoi(m[2])
		}
		if season > 0 && episode > 0 {
			meta["videos"] = []fiber.Map{{
				"id":    id + ":" + strconv.Itoa(season) + ":" + strconv.Itoa(episode),
				"title": name, "season": season, "episode": episode,
			}}
		}
	}
	raw, _ := json.Marshal(meta)
	return database.LibraryTitle{Type: mediaType, IMDbID: id, Name: name, Metadata: raw}, season, episode, true
}

// RegisterLibraryAutosave wires import completion to best-effort library
// auto-save and starts the hourly healthy-library auto-import when enabled.
// The hook never fails an import: it runs detached and logs.
func (s *Server) RegisterLibraryAutosave(ctx context.Context) {
	if s.importerService == nil {
		return
	}
	s.importerService.SetImportCompletedHook(func(hookCtx context.Context, queueID int64) {
		s.autoSaveCompletedImport(hookCtx, queueID)
	})
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.configManager == nil {
					continue
				}
				// Master-switch: the hourly auto-import is an automatic path; it
				// must not reach provider metadata APIs while the Stremio
				// integration is disabled, regardless of its own opt-in flag.
				cfg := s.configManager.GetConfig()
				if !cfg.EffectiveLibraryAutoImportHealth() || !isStremioEnabled(cfg) {
					continue
				}
				importCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
				totalImported, totalSkipped := 0, 0
				for batch := 0; batch < 8; batch++ {
					imported, skipped, complete, err := s.importHealthyLibraryFiles(importCtx)
					if err != nil {
						slog.WarnContext(ctx, "Library health auto-import failed", "error", err)
						break
					}
					totalImported += imported
					totalSkipped += skipped
					if complete {
						break
					}
				}
				cancel()
				if totalImported > 0 {
					slog.InfoContext(ctx, "Library health auto-import completed",
						"imported", totalImported, "skipped", totalSkipped)
				}
			}
		}
	}()
}

func (s *Server) autoSaveCompletedImport(ctx context.Context, queueID int64) {
	if s.configManager == nil || s.queueRepo == nil {
		return
	}
	cfg := s.configManager.GetConfig()
	// Master-switch: this hook is automatic (fires on every completed import);
	// saving and the Cinemeta enrichment below stay dormant while the Stremio
	// integration is disabled, whatever the autosave scope says.
	if !isStremioEnabled(cfg) {
		return
	}
	scope := cfg.EffectiveLibraryAutoSave()
	if scope == "off" {
		return
	}
	item, err := s.queueRepo.GetQueueItem(ctx, queueID)
	if err != nil || item == nil {
		return
	}
	if item.Status != database.QueueStatusCompleted || item.StoragePath == nil || *item.StoragePath == "" {
		return
	}
	source := classifyLibraryItem(item)
	if !libraryAutoSaveScopeAllows(scope, source) {
		return
	}
	title, season, episode, ok := libraryTitleForAutosave(item)
	if !ok {
		slog.WarnContext(ctx, "Library auto-save skipped: release not identifiable",
			"queue_id", queueID, "source", source)
		return
	}
	if strings.HasPrefix(title.IMDbID, "tt") {
		title = enrichLibraryTitle(ctx, title)
	}
	database.LibraryAssetMu.Lock()
	defer database.LibraryAssetMu.Unlock()
	if _, err := s.queueRepo.SaveLibraryRelease(ctx, item.ID, title, season, episode); err != nil {
		slog.WarnContext(ctx, "Library auto-save failed",
			"queue_id", queueID, "source", source, "error", err)
		return
	}
	slog.InfoContext(ctx, "Library auto-saved completed import",
		"queue_id", queueID, "source", source, "title", title.Name)
}

// releaseOrigin classifies a release's provenance for display tags: stremio
// (Stremio download IDs or Stremio-shaped metadata), arrs (SABnzbd ARR
// markers or ARR webhook metadata), or manual for everything else.
func releaseOrigin(r *database.LibraryRelease) string {
	if strings.HasPrefix(r.DownloadID, stremioDownloadIDPrefix) {
		return "stremio"
	}
	if r.Snapshot.Metadata != nil {
		var sq stremioQueueMetadata
		if err := json.Unmarshal([]byte(*r.Snapshot.Metadata), &sq); err == nil {
			if libraryIMDbPattern.MatchString(sq.IMDbID) {
				return "stremio"
			}
		}
		if title, _ := arrTitleFromMetadata(r.Snapshot.Metadata); title != "" {
			return "arrs"
		}
	}
	if r.HealthMetadata != nil && strings.TrimSpace(*r.HealthMetadata) != "" {
		var shape map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*r.HealthMetadata), &shape); err == nil {
			if _, ok := shape["imdb_id"]; ok {
				return "stremio"
			}
			if _, ok := shape["movie"]; ok {
				return "arrs"
			}
			if _, ok := shape["series"]; ok {
				return "arrs"
			}
		}
	}
	return "manual"
}

// annotateLibraryOrigin fills the Origin display tag of each release.
func annotateLibraryOrigin(releases []database.LibraryRelease) {
	for i := range releases {
		releases[i].Origin = releaseOrigin(&releases[i])
	}
}

// libraryContentIdentity builds the canonical grouping key for a release:
// movies match on type+IMDb, episodes on type+IMDb+season+episode. Missing
// season/episode on a series release yields "" (unverified, never anchors).
// Used for grouping/dedup only — Stremio anchoring is decided by
// libraryStremioAnchor, and completion keeps all releases under an anchored
// title regardless of origin.
func libraryContentIdentity(mediaType, imdbID string, season, episode int) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	imdbID = strings.ToLower(strings.TrimSpace(imdbID))
	if mediaType == "" || imdbID == "" {
		return ""
	}
	if mediaType == "series" {
		if season > 0 && episode > 0 {
			return mediaType + "|" + imdbID + "|" + strconv.Itoa(season) + "|" + strconv.Itoa(episode)
		}
		return ""
	}
	return mediaType + "|" + imdbID
}

// libraryStremioAnchor reports whether a release is a valid Stremio anchor:
// canonical tt… title, supported type, stremio origin, and (for series) a
// verified S>0/E>0 episode. Movies anchor on type|tt; series anchor per
// verified episode. Synthetic altm-* titles never anchor until relinked.
func libraryStremioAnchor(mediaType, imdbID string, r database.LibraryRelease) bool {
	if !libraryIMDbPattern.MatchString(imdbID) {
		return false
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "movie" && mediaType != "series" {
		return false
	}
	if r.Origin != librarySourceStremio {
		return false
	}
	if mediaType == "series" && (r.Season <= 0 || r.Episode <= 0) {
		return false
	}
	return true
}

// LibraryStremioComplete returns Stremio-anchored titles with ALL their
// releases (completion semantics): a movie/show appears when ≥1 Stremio
// release anchors it, and keeps every file under it regardless of origin —
// so Stremio plays complete with ARR/manual files. Titles with no Stremio
// anchor (ARR/manual-only, synthetic, unverified-only) are excluded.
// Once any verified Stremio episode anchors a series, the whole show is
// retained including other episodes and unverified releases.
// Real-IMDb titles are the explicit exception the collection owner asked
// for: a tt-keyed title carrying ANY release (altmount instant/ARR/health
// backfills) stays visible even without a Stremio anchor, so instant files
// join their Stremio siblings under one tile; the automated repair pass
// relabels synthetic look-alikes to exactly these tt keys. Synthetic
// altm-* rows without a resolved identity still stay hidden.
func LibraryStremioComplete(titles []database.LibraryTitle) []database.LibraryTitle {
	out := []database.LibraryTitle{}
	for i := range titles {
		for j := range titles[i].Releases {
			if titles[i].Releases[j].Origin == "" {
				annotateLibraryOrigin(titles[i].Releases)
				break
			}
		}
		anchored := false
		for _, r := range titles[i].Releases {
			if libraryStremioAnchor(titles[i].Type, titles[i].IMDbID, r) {
				anchored = true
				break
			}
		}
		if !anchored && libraryIMDbPattern.MatchString(titles[i].IMDbID) && len(titles[i].Releases) > 0 {
			anchored = true
		}
		if anchored {
			out = append(out, titles[i])
		}
	}
	return out
}

// LibraryStremioOnly is kept for compatibility; completion semantics apply.
func LibraryStremioOnly(titles []database.LibraryTitle) []database.LibraryTitle {
	return LibraryStremioComplete(titles)
}
