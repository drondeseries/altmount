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
	"github.com/kipsilabs/altmount/internal/arrs/model"
	"github.com/kipsilabs/altmount/internal/database"
	"github.com/kipsilabs/altmount/internal/stremio"
)

// healthImportBatchCap bounds one import run (manual or automatic) so a huge
// library cannot stall a request or hammer metadata providers. Repeat runs
// resume where the previous one stopped because imported rows are skipped.
const healthImportBatchCap = 250

type tvmazeIMDbHit struct {
	IMDbID string
	Name   string
}

var tvdbIMDbCache seriesMetadataCache[tvmazeIMDbHit]

// resolveIMDbFromTVDB maps a TVDB series ID to its IMDb ID and canonical
// name via TVMaze. Results (including misses) are cached. Empty IMDbID means
// "unknown", not an error callers must surface.
func resolveIMDbFromTVDB(ctx context.Context, tvdbID int64, userAgent string) tvmazeIMDbHit {
	key := strconv.FormatInt(tvdbID, 10)
	if cached, ok := tvdbIMDbCache.get(key); ok {
		return cached
	}
	miss := tvmazeIMDbHit{}
	if tvdbID <= 0 {
		tvdbIMDbCache.put(key, miss)
		return miss
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(lookupCtx, http.MethodGet,
		tvmazeBaseURL+"/lookup/shows?thetvdb="+url.QueryEscape(key), nil)
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
	if resp.StatusCode == http.StatusNotFound {
		tvdbIMDbCache.put(key, miss)
		return miss
	}
	if resp.StatusCode != http.StatusOK {
		return miss
	}
	var show struct {
		Name      string `json:"name"`
		Externals struct {
			IMDb string `json:"imdb"`
		} `json:"externals"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetadataBytes)).Decode(&show); err != nil {
		tvdbIMDbCache.put(key, miss)
		return miss
	}
	hit := tvmazeIMDbHit{IMDbID: strings.TrimSpace(show.Externals.IMDb), Name: strings.TrimSpace(show.Name)}
	if !libraryIMDbPattern.MatchString(hit.IMDbID) {
		hit.IMDbID = ""
	}
	tvdbIMDbCache.put(key, hit)
	return hit
}

// healthDisplayPath prefers the ARR-managed library path (clean
// "Title (Year)" folders) over the raw virtual file path for parsing.
func healthDisplayPath(rec *database.FileHealth) string {
	if rec.LibraryPath != nil && *rec.LibraryPath != "" && rec.IsImported() {
		return *rec.LibraryPath
	}
	return rec.FilePath
}

// segmentNamePattern matches bare disc-segment filenames (00000.m2ts):
// never a real title, always part of a larger release.
var segmentNamePattern = regexp.MustCompile(`^0*[0-9]{1,5}$`)

// healthIdentity extracts title identity from a file_health record without
// any network calls: a Stremio IMDb ID when the row came from a Stremio
// import, else ARR TMDB/TVDB IDs, else the parsed filename.
type healthIdentity struct {
	mediaType       string
	imdbID          string
	tmdbID, tvdbID  int64
	season, episode int
	name            string
	// displayName overrides the release display name when the file itself is
	// obfuscated (hash-like) but a parent folder carries the real release name.
	displayName string
	parsed      stremio.ParsedRelease
}

// obfuscatedNamePattern matches hash-like obfuscated filenames (32-hex NZBgeek
// parts): never a real title.
var obfuscatedNamePattern = regexp.MustCompile(`^[0-9a-fA-F]{12,}$`)

func isObfuscatedName(base string) bool {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return true
	}
	return obfuscatedNamePattern.MatchString(trimmed)
}

func healthIdentityForRecord(rec *database.FileHealth) (healthIdentity, bool) {
	var id healthIdentity
	if rec == nil {
		return id, false
	}
	// Stremio-imported rows carry the real IMDb ID in their metadata.
	var sq stremioQueueMetadata
	if rec.Metadata != nil && *rec.Metadata != "" {
		if err := json.Unmarshal([]byte(*rec.Metadata), &sq); err == nil &&
			libraryIMDbPattern.MatchString(sq.IMDbID) && (sq.Type == "movie" || sq.Type == "series") {
			id.mediaType = sq.Type
			id.imdbID = sq.IMDbID
			id.season, id.episode = sq.Season, sq.Episode
		}
	}
	var meta model.HealthMetadata
	if rec.Metadata != nil && *rec.Metadata != "" {
		meta, _ = model.DecodeHealthMetadata(*rec.Metadata)
	}
	display := healthDisplayPath(rec)
	base := strings.TrimSuffix(filepath.Base(display), filepath.Ext(display))
	if isObfuscatedName(base) {
		// Obfuscated filenames (hash-like NZBgeek parts) carry no identity:
		// walk up to 3 parents for the real release folder name.
		dir := filepath.Dir(display)
		for i := 0; i < 3 && dir != "." && dir != "/"; i++ {
			candidate := filepath.Base(dir)
			if !isObfuscatedName(candidate) && len(candidate) > 3 {
				base = candidate
				id.displayName = candidate
				break
			}
			dir = filepath.Dir(dir)
		}
	}
	parsed := stremio.ParseReleaseTitle(base)
	id.parsed = parsed
	if id.imdbID == "" {
		id.tmdbID = meta.TMDBID
		if id.tmdbID <= 0 && meta.Movie != nil {
			id.tmdbID = meta.Movie.TMDBID
		}
		if id.tmdbID <= 0 {
			id.tmdbID = meta.LegacyTMDBID
			if id.tmdbID <= 0 && meta.Movie != nil {
				id.tmdbID = meta.Movie.LegacyTMDBID
			}
		}
		id.tvdbID = meta.TVDBID
		if id.tvdbID <= 0 && meta.Series != nil {
			id.tvdbID = meta.Series.TVDBID
		}
		if id.tvdbID <= 0 {
			id.tvdbID = meta.LegacyTVDBID
			if id.tvdbID <= 0 && meta.Series != nil {
				id.tvdbID = meta.Series.LegacyTVDBID
			}
		}
		switch {
		case id.tmdbID > 0:
			id.mediaType = "movie"
		case id.tvdbID > 0:
			id.mediaType = "series"
		case parsed.Season > 0 || parsed.EpisodeStart > 0 || parsed.IsSeasonPack:
			id.mediaType = "series"
		default:
			id.mediaType = "movie"
		}
		id.season, id.episode = parsed.Season, parsed.EpisodeStart
	}
	name := strings.TrimSpace(parsed.SeriesTitle)
	if name == "" {
		name = strings.TrimSpace(parsed.CleanTitle)
	}
	if name == "" {
		name = base
	}
	id.name = name
	return id, true
}

// healthTitleForRecord builds a library title for a healthy file_health
// record: Stremio IMDb IDs and ARR TMDB/TVDB identifiers first, parsed
// filename second. Series TVDB IDs resolve to IMDb via TVMaze; movies without
// an IMDb ID get a stable tmdb-anchored synthetic ID so renames keep grouping.
func healthTitleForRecord(ctx context.Context, rec *database.FileHealth, userAgent string) (database.LibraryTitle, int, int, bool) {
	id, ok := healthIdentityForRecord(rec)
	if !ok {
		return database.LibraryTitle{}, 0, 0, false
	}
	return healthTitleFromIdentity(ctx, id, userAgent)
}

// healthTitleFromIdentity resolves a precomputed identity into a title,
// hitting the network only for TVDB→IMDb resolution.
func healthTitleFromIdentity(ctx context.Context, id healthIdentity, userAgent string) (database.LibraryTitle, int, int, bool) {
	mediaType, tmdbID, tvdbID := id.mediaType, id.tmdbID, id.tvdbID
	name, parsed := id.name, id.parsed
	if id.imdbID != "" {
		t := database.LibraryTitle{Type: mediaType, IMDbID: id.imdbID, Name: name}
		raw, _ := json.Marshal(fiber.Map{"id": id.imdbID, "type": mediaType, "name": name})
		t.Metadata = raw
		return t, id.season, id.episode, true
	}

	season, episode := id.season, id.episode

	if mediaType == "series" && tvdbID > 0 {
		if hit := resolveIMDbFromTVDB(ctx, tvdbID, userAgent); hit.IMDbID != "" {
			if hit.Name != "" {
				name = hit.Name
			}
			t := database.LibraryTitle{Type: "series", IMDbID: hit.IMDbID, Name: name}
			raw, _ := json.Marshal(fiber.Map{"id": hit.IMDbID, "type": "series", "name": name})
			t.Metadata = raw
			return t, season, episode, true
		}
		id := librarySyntheticIDPrefix + "series-tvdb-" + strconv.FormatInt(tvdbID, 10)
		return syntheticHealthTitle("series", id, name, season, episode, parsed), season, episode, true
	}
	if mediaType == "movie" && tmdbID > 0 {
		id := librarySyntheticIDPrefix + "movie-tmdb-" + strconv.FormatInt(tmdbID, 10)
		return syntheticHealthTitle("movie", id, name, 0, 0, parsed), 0, 0, true
	}
	return syntheticHealthTitle(mediaType, librarySyntheticIDPrefix+mediaType+"-"+librarySlug(name), name, season, episode, parsed), season, episode, name != ""
}

func syntheticHealthTitle(mediaType, id, name string, season, episode int, parsed stremio.ParsedRelease) database.LibraryTitle {
	meta := fiber.Map{"id": id, "type": mediaType, "name": name}
	if parsed.Year > 0 {
		meta["releaseInfo"] = strconv.Itoa(parsed.Year)
	}
	if mediaType == "series" && season > 0 && episode > 0 {
		meta["videos"] = []fiber.Map{{
			"id":    id + ":" + strconv.Itoa(season) + ":" + strconv.Itoa(episode),
			"title": name, "season": season, "episode": episode,
		}}
	}
	raw, _ := json.Marshal(meta)
	return database.LibraryTitle{Type: mediaType, IMDbID: id, Name: name, Metadata: raw}
}

// importHealthyLibraryFiles pins playable file_health records ("Local
// Library" files) into the saved library. Idempotent: already-imported files
// and files covered by queue-sourced saves are skipped. Bounded per run.
func (s *Server) importHealthyLibraryFiles(ctx context.Context) (imported, skipped int, complete bool, err error) {
	if s.queueRepo == nil || s.healthRepo == nil || s.configManager == nil {
		return 0, 0, true, nil
	}
	records, err := s.healthRepo.ListHealthyLibraryFiles(ctx, healthImportBatchCap*8)
	if err != nil {
		return 0, 0, true, err
	}
	refs, err := s.queueRepo.LibraryReleases(ctx)
	if err != nil {
		return 0, 0, true, err
	}
	importedHealth := map[int64]struct{}{}
	savedDownloads := map[string]struct{}{}
	for _, ref := range refs {
		if ref.FileHealthID != nil {
			importedHealth[*ref.FileHealthID] = struct{}{}
		}
		if ref.DownloadID != "" {
			savedDownloads[ref.DownloadID] = struct{}{}
		}
	}
	// Remove segment-named releases our earlier imports created (bare
	// "00012" titles from Blu-ray .m2ts parts). They carry no real identity.
	cleaned := 0
	for _, ref := range refs {
		if ref.Source == "health" && ref.Saved && !strings.HasPrefix(ref.IMDbID, "tt") &&
			segmentNamePattern.MatchString(ref.Name) {
			if err := s.queueRepo.DeleteLibraryRelease(ctx, ref.ID); err == nil {
				cleaned++
			}
		}
	}
	if cleaned > 0 {
		slog.InfoContext(ctx, "Library health import removed segment releases", "count", cleaned)
	}
	userAgent := s.configManager.GetConfig().GetUserAgent()
	// Group rows first (no network): one release per download for movies and
	// season packs, one per episode for series. Disc segments sharing a
	// download collapse into a single movie release.
	type group struct {
		rec *database.FileHealth
		id  healthIdentity
	}
	groups := map[string]*group{}
	order := []string{}
	for _, rec := range records {
		if rec == nil {
			continue
		}
		if _, dup := importedHealth[rec.ID]; dup {
			skipped++
			continue
		}
		downloadKey := ""
		if rec.DownloadID != nil {
			downloadKey = *rec.DownloadID
		}
		if _, dup := savedDownloads[downloadKey]; dup && downloadKey != "" {
			skipped++
			continue
		}
		id, ok := healthIdentityForRecord(rec)
		if !ok {
			skipped++
			continue
		}
		key := healthGroupKey(rec, id, downloadKey)
		if g, exists := groups[key]; exists {
			// Prefer the row carrying a real IMDb ID, else the most
			// descriptive non-segment name, for the group's identity.
			gHasIMDb, hasIMDb := groupHasIMDb(g.id), groupHasIMDb(id)
			if !gHasIMDb && (hasIMDb ||
				(isSegmentName(g.id.name) && !isSegmentName(id.name)) ||
				(!isSegmentName(id.name) && len(id.name) > len(g.id.name))) {
				g.rec, g.id = rec, id
			}
			skipped++
			continue
		}
		groups[key] = &group{rec: rec, id: id}
		order = append(order, key)
	}
	processed := 0
	for _, key := range order {
		g := groups[key]
		rec := g.rec
		if processed >= healthImportBatchCap {
			break
		}
		processed++
		// Prefer the import-queue row behind this download: it carries the
		// exact Stremio/ARR identity even when the health row's metadata is
		// empty (e.g. obfuscated filenames).
		var title database.LibraryTitle
		var season, episode int
		var ok bool
		var queued *database.ImportQueueItem
		if rec.DownloadID != nil && *rec.DownloadID != "" {
			queued, _ = s.queueRepo.GetQueueItemByDownloadID(ctx, *rec.DownloadID)
		}
		if queued != nil {
			if t, se, ep, tok := libraryTitleForAutosave(queued); tok {
				title, season, episode, ok = t, se, ep, tok
			}
		}
		if !ok {
			title, season, episode, ok = healthTitleFromIdentity(ctx, g.id, userAgent)
		}
		if !ok {
			skipped++
			continue
		}
		if strings.HasPrefix(title.IMDbID, "tt") {
			title = enrichLibraryTitle(ctx, title)
		} else {
			title = enrichLibraryTitleBySearch(ctx, title, userAgent)
		}
		nzbPath := ""
		if rec.SourceNzbPath != nil {
			nzbPath = *rec.SourceNzbPath
		}
		downloadID := ""
		if rec.DownloadID != nil {
			downloadID = *rec.DownloadID
		}
		// Obfuscated or segment filenames carry no title info: name the
		// release after the informative parent folder (or identity-derived
		// display name) so quality tags stay parseable.
		releaseName := g.id.displayName
		if releaseName == "" {
			segmentBase := strings.TrimSuffix(filepath.Base(rec.FilePath), filepath.Ext(rec.FilePath))
			if isSegmentName(segmentBase) || isObfuscatedName(segmentBase) {
				if parent := filepath.Base(filepath.Dir(rec.FilePath)); parent != "" && parent != "." && parent != "/" {
					releaseName = parent
				}
			}
		}
		database.LibraryAssetMu.Lock()
		_, saveErr := s.queueRepo.ImportHealthRelease(ctx, rec.ID, rec.FilePath, title, season, episode, downloadID, nzbPath, releaseName)
		database.LibraryAssetMu.Unlock()
		if saveErr != nil {
			slog.WarnContext(ctx, "Library health import failed", "file", rec.FilePath, "error", saveErr)
			skipped++
			continue
		}
		imported++
		if downloadID != "" {
			savedDownloads[downloadID] = struct{}{}
		}
		importedHealth[rec.ID] = struct{}{}
	}
	// NOTE: stale synthetic retry now runs exclusively through the ledger
	// scheduler (runLibraryRepairPass, phase-2 of startLibraryImportHealth).
	// The old refreshStaleLibraryTitles loop is retired — see below.
	return imported, skipped, processed < healthImportBatchCap, nil
}

func groupHasIMDb(id healthIdentity) bool { return id.imdbID != "" }

func isSegmentName(name string) bool { return segmentNamePattern.MatchString(name) }

// healthGroupKey groups rows into one release: one per download for movies
// and season packs (disc segments collapse), one per episode for series.
func healthGroupKey(rec *database.FileHealth, id healthIdentity, downloadKey string) string {
	key := downloadKey
	if key == "" {
		if rec.SourceNzbPath != nil {
			key = *rec.SourceNzbPath
		} else {
			key = rec.FilePath
		}
	}
	if id.mediaType == "series" && (id.season > 0 || id.episode > 0) {
		key += "|" + strconv.Itoa(id.season) + ":" + strconv.Itoa(id.episode)
	} else {
		key += "|feature"
	}
	return key
}

// importHealthState tracks the background library import across requests.
type importHealthState struct {
	running      bool
	imported     int
	skipped      int
	refreshed    int
	relinked     int
	posters      int
	complete     bool
	updatedAt    time.Time
	errorMessage string
}

// startLibraryImportHealth runs batched imports in the background (10-minute
// budget) so large backfills never block the request behind proxy timeouts.
func (s *Server) startLibraryImportHealth(ctx context.Context) bool {
	s.importHealthMu.Lock()
	if s.importHealth.running {
		s.importHealthMu.Unlock()
		return false
	}
	s.importHealth.running = true
	s.importHealthMu.Unlock()
	go func() {
		defer func() {
			s.importHealthMu.Lock()
			s.importHealth.running = false
			s.importHealth.updatedAt = time.Now()
			s.importHealthMu.Unlock()
		}()
		workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		for {
			imported, skipped, complete, err := s.importHealthyLibraryFiles(workCtx)
			s.importHealthMu.Lock()
			s.importHealth.imported += imported
			s.importHealth.skipped += skipped
			s.importHealth.complete = complete
			s.importHealth.updatedAt = time.Now()
			if err != nil {
				s.importHealth.errorMessage = err.Error()
			}
			s.importHealthMu.Unlock()
			if err != nil {
				return
			}
			if !complete {
				continue
			}
			// Phase two of the same run, once imports settle: durable repair
			// pass over posterless titles (ledger-backed, fair, observable).
			// Replaces the unmarked refreshPosterless retry loop. Shares the
			// single repair slot with the automatic loop: when that pass is
			// in flight, this one is skipped instead of double-leasing rows;
			// skipped rows are picked up on the next scheduled pass.
			if !s.claimLibraryRepairSlot() {
				slog.InfoContext(workCtx, "Skipping phase-2 library repair pass: automatic repair pass already in flight")
				return
			}
			counters := s.runLibraryRepairPass(workCtx, s.configManager.GetConfig().GetUserAgent())
			s.releaseLibraryRepairSlot()
			s.importHealthMu.Lock()
			s.importHealth.relinked += counters.relinked
			s.importHealth.posters += counters.posterAdd
			s.importHealth.refreshed += counters.relinked + counters.posterAdd
			s.importHealth.updatedAt = time.Now()
			s.importHealthMu.Unlock()
			return
		}
	}()
	return true
}

// refreshPosterlessLibraryTitles re-enriches saved titles whose stored
// metadata has no usable poster (real tt… IDs whose fetch failed or whose
// metadata predates enrichment). Bounded per run; retried titles that gain a
// poster are persisted via UpdateLibraryTitleMetadata and stop being retried.
// Returns (relinked, posters).
//
// NOTE: failures are deliberately NOT marked anywhere. A title that cannot
// be enriched now (provider down, unknown ID) simply shows up again on the
// next run; with caps of a few hundred per pass and the existing
// search-cache, a full backlog converges without hammering providers.
//
// DEPRECATED: superseded by runLibraryRepairPass (library_repair_scheduler.go),
// which uses the durable ledger (044) for fairness, backoff, and review.
// Kept for manual callers until the scheduler ships everywhere.
func (s *Server) refreshPosterlessLibraryTitles(ctx context.Context, userAgent string) (relinked, posters int) {
	if s.queueRepo == nil {
		return 0, 0
	}
	titles, err := s.queueRepo.LibraryTitles(ctx)
	if err != nil {
		return 0, 0
	}
	for _, t := range titles {
		if relinked+posters >= libraryPosterRefreshCap {
			break
		}
		if libraryTitleHasPoster(t.Metadata) {
			continue
		}
		if strings.HasPrefix(t.IMDbID, "tt") {
			// Real IMDb identity, thin/absent metadata: re-fetch the record
			// (same Cinemeta path as the save flow) and persist it. This
			// only writes on success, so repeated failures just retry.
			enriched := enrichLibraryTitle(ctx, database.LibraryTitle{
				Type: t.Type, IMDbID: t.IMDbID, Name: t.Name, Metadata: t.Metadata,
			})
			if !libraryTitleHasPoster(enriched.Metadata) {
				continue
			}
			if err := s.queueRepo.UpdateLibraryTitleMetadata(ctx, t.Type, t.IMDbID, enriched.Name, enriched.Metadata); err != nil {
				slog.WarnContext(ctx, "Library poster refresh failed", "title", t.Name, "error", err)
				continue
			}
			slog.InfoContext(ctx, "Library poster refreshed", "title", enriched.Name, "id", enriched.IMDbID)
			posters++
			continue
		}
		// Synthetic identity: same search-fallback resolution as the save
		// flow. Persists only the full enrichment (metadata incl. poster);
		// a resolved-but-still-posterless record contributes nothing.
		enriched := enrichLibraryTitleBySearch(ctx, database.LibraryTitle{
			Type: t.Type, IMDbID: t.IMDbID, Name: t.Name, Metadata: t.Metadata,
		}, userAgent)
		if !strings.HasPrefix(enriched.IMDbID, "tt") || enriched.IMDbID == t.IMDbID {
			continue
		}
		if !libraryTitleHasPoster(enriched.Metadata) {
			continue
		}
		if err := s.queueRepo.RelinkLibraryTitle(ctx, t.Type, t.IMDbID, enriched); err != nil {
			slog.WarnContext(ctx, "Library title refresh failed", "title", t.Name, "error", err)
			continue
		}
		slog.InfoContext(ctx, "Library title refreshed with resolved metadata",
			"from", t.IMDbID, "to", enriched.IMDbID, "title", enriched.Name)
		relinked++
	}
	return relinked, posters
}

// libraryTitleHasPoster reports whether stored title metadata carries a
// usable poster URL (the field the grid reads through meta.poster). Missing
// key, null, non-string, and whitespace-only values all count as absent.
func libraryTitleHasPoster(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var meta struct {
		Poster *string `json:"poster"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return false
	}
	return meta.Poster != nil && strings.TrimSpace(*meta.Poster) != ""
}

// libraryPosterRefreshCap bounds the combined tt… poster backfill +
// synthetic-resolution pass per run. Refreshing within the manual/health
// batch keeps a single 10-minute import window from being starved by metadata
// lookups (~2 HTTP calls per title worst case); repeat runs resume because
// fixed titles gain posters and drop out of the candidate set.
const libraryPosterRefreshCap = 200

// refreshStaleLibraryTitles is RETIRED. All synthetic retry now runs
// through the ledger scheduler (runLibraryRepairPass): evidence-gated,
// backoff-scheduled, poster-required, and review-parked. This stub remains
// so any external caller compiles; it performs no work.
func (s *Server) refreshStaleLibraryTitles(ctx context.Context, userAgent string) int {
	_ = ctx
	_ = userAgent
	return 0
}

func (s *Server) handleLibraryImportHealth(c *fiber.Ctx) error {
	if !s.startLibraryImportHealth(c.Context()) {
		return c.JSON(fiber.Map{"data": fiber.Map{"running": true}})
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"data": fiber.Map{"started": true}})
}

func (s *Server) handleLibraryImportHealthStatus(c *fiber.Ctx) error {
	s.importHealthMu.Lock()
	state := s.importHealth
	s.importHealthMu.Unlock()
	return c.JSON(fiber.Map{"data": fiber.Map{
		"running":   state.running,
		"imported":  state.imported,
		"skipped":   state.skipped,
		"refreshed": state.refreshed,
		"relinked":  state.relinked,
		"posters":   state.posters,
		"complete":  state.complete,
		"error":     state.errorMessage,
		"updatedAt": state.updatedAt.Unix(),
	}})
}
