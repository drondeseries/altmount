package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kipsilabs/altmount/internal/database"
)

var libraryIMDbPattern = regexp.MustCompile(`^tt[0-9]{5,12}$`)

// libraryDigest is the compact per-title payload for the embedded Stremio
// Library grid: identity + artwork + the aggregate values the client filters
// and sorts on (release_count, size, recency, genres). Full Cinemeta
// metadata and release rows stay on the detail endpoints; a full digest of
// a 5k-title library is <1 MB instead of ~60 MB of JSON.
type libraryDigest struct {
	Type            string   `json:"type"`
	IMDbID          string   `json:"imdb_id"`
	Title           string   `json:"title"`
	Poster          string   `json:"poster,omitempty"`
	Background      string   `json:"background,omitempty"`
	Year            string   `json:"year,omitempty"`
	Rating          string   `json:"rating,omitempty"`
	Genres          []string `json:"genres,omitempty"`
	ReleaseCount    int      `json:"release_count"`
	TotalSize       int64    `json:"total_size"`
	MaxImportedAt   int64    `json:"max_imported_at"`
	MaxLastVerified int64    `json:"max_last_verified"`
}

// libraryReleaseDigest contains per-title aggregates mirrored from the
// client-side titleSize/titleMetric computations over full release lists.
func libraryDigestFromTitle(t database.LibraryTitle) libraryDigest {
	d := libraryDigest{
		Type:         t.Type,
		IMDbID:       t.IMDbID,
		Title:        t.Name,
		ReleaseCount: len(t.Releases),
	}
	for i := range t.Releases {
		d.TotalSize += t.Releases[i].Size
		if t.Releases[i].ImportedAt > d.MaxImportedAt {
			d.MaxImportedAt = t.Releases[i].ImportedAt
		}
		if t.Releases[i].LastVerified > d.MaxLastVerified {
			d.MaxLastVerified = t.Releases[i].LastVerified
		}
	}
	if len(t.Metadata) > 0 {
		var meta struct {
			Name        string          `json:"name"`
			Poster      string          `json:"poster"`
			Background  string          `json:"background"`
			ReleaseInfo json.RawMessage `json:"releaseInfo"`
			Year        json.RawMessage `json:"year"`
			IMDBRating  json.RawMessage `json:"imdbRating"`
			Genres      []string        `json:"genres"`
		}
		if json.Unmarshal(t.Metadata, &meta) == nil {
			if strings.TrimSpace(meta.Name) != "" {
				d.Title = strings.TrimSpace(meta.Name)
			}
			d.Poster = strings.TrimSpace(meta.Poster)
			d.Background = strings.TrimSpace(meta.Background)
			d.Year = libraryReleaseInfoYear(meta.ReleaseInfo, meta.Year)
			d.Rating = libraryRawString(meta.IMDBRating)
			d.Genres = meta.Genres
		}
	}
	return d
}

// libraryReleaseInfoYear mirrors the client's String(releaseInfo || year):
// strings pass through, arrays join with commas, numbers stringify, nulls
// and objects/unknown shapes fall back to the year field (never "[object
// Object]", and a literal null never degrades to "0").
func libraryReleaseInfoYear(releaseInfo, year []byte) string {
	if len(releaseInfo) > 0 {
		var anyVal any
		if json.Unmarshal(releaseInfo, &anyVal) == nil {
			switch val := anyVal.(type) {
			case string:
				if s := strings.TrimSpace(val); s != "" {
					return s
				}
			case []any:
				parts := make([]string, 0, len(val))
				for _, v := range val {
					switch item := v.(type) {
					case string:
						parts = append(parts, item)
					case float64:
						parts = append(parts, strconv.FormatFloat(item, 'g', -1, 64))
					}
				}
				return strings.Join(parts, ",")
			case float64:
				if val > 0 {
					return strconv.FormatFloat(val, 'g', -1, 64)
				}
			}
			// null, empty string, zero, objects, unknowns fall through.
		}
	}
	return libraryRawString(year)
}

// libraryRawString stringifies a JSON scalar the way JS String() does; null
// and unknown shapes yield "" so the year field fallback still applies.
func libraryRawString(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var anyVal any
	if json.Unmarshal(raw, &anyVal) != nil || anyVal == nil {
		return ""
	}
	switch val := anyVal.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'g', -1, 64)
	}
	return ""
}

func isLibraryTableMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "undefined table") || strings.Contains(msg, "unknown table")
}

func libraryTitleForItem(item *database.ImportQueueItem) (database.LibraryTitle, stremioQueueMetadata, bool) {
	var m stremioQueueMetadata
	if item.Metadata == nil || json.Unmarshal([]byte(*item.Metadata), &m) != nil || !libraryIMDbPattern.MatchString(m.IMDbID) || (m.Type != "movie" && m.Type != "series") {
		return database.LibraryTitle{}, m, false
	}
	name := strings.TrimSuffix(filepath.Base(item.NzbPath), ".nzb")
	meta := fiber.Map{"id": m.IMDbID, "type": m.Type, "name": name}
	if m.Type == "series" && m.Season > 0 && m.Episode > 0 {
		meta["videos"] = []fiber.Map{{"id": m.IMDbID + ":" + strconv.Itoa(m.Season) + ":" + strconv.Itoa(m.Episode), "title": name, "season": m.Season, "episode": m.Episode}}
	}
	raw, _ := json.Marshal(meta)
	return database.LibraryTitle{Type: m.Type, IMDbID: m.IMDbID, Name: name, Metadata: raw}, m, true
}

// Cinemeta is optional: bounded request and body, strict identity validation, durable fallback.
func enrichLibraryTitle(ctx context.Context, t database.LibraryTitle) database.LibraryTitle {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cinemetaBaseURL+"/meta/"+t.Type+"/"+t.IMDbID+".json", nil)
	if err != nil {
		return t
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return t
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return t
	}
	var body struct {
		Meta json.RawMessage `json:"meta"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&body) != nil {
		return t
	}
	var meta struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(body.Meta, &meta) != nil || meta.ID != t.IMDbID || meta.Type != t.Type || strings.TrimSpace(meta.Name) == "" {
		return t
	}
	t.Name = meta.Name
	t.Metadata = body.Meta
	return t
}

// metahubBaseURL is the artwork CDN root, which serves images directly by
// IMDb id (poster/logo/background, medium quality) — no name/year lookup and
// subject to very lax rate limits, unlike Cinemeta, which floods or 403s
// plain HTTP bursts. A variable so tests can substitute a fake CDN.
var metahubBaseURL = "https://live.metahub.space"

// metahubArtwork probes CDN artwork URLs for an IMDb id. poster is required
// (its 200 makes the row fixable); logo and background are best-effort. The
// shared network pause applies so a fallback pass keeps the enriched burst
// at the same polite cadence.
func metahubArtwork(ctx context.Context, imdbID string) (poster, logo, background string, ok bool) {
	if !pauseRepairNetwork(ctx) {
		return "", "", "", false
	}
	probe := func(quality string) string {
		url := metahubBaseURL + "/" + quality + "/medium/" + url.PathEscape(imdbID) + "/img"
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			return ""
		}
		client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := client.Do(req)
		if err != nil {
			return ""
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return ""
		}
		return url
	}
	poster = probe("poster")
	if poster == "" {
		return "", "", "", false
	}
	return poster, probe("logo"), probe("background"), true
}

// applyMetahubFallback merges CDN artwork into a title's metadata when
// Cinemeta enrichment has no poster: the tile needs its visual first, full
// metadata re-enrichment follows whenever Cinemeta answers again. Name and
// any existing fields (releaseInfo, videos) are preserved.
func applyMetahubFallback(ctx context.Context, imdbID, mediaType string, base database.LibraryTitle) (database.LibraryTitle, bool) {
	poster, logo, background, ok := metahubArtwork(ctx, imdbID)
	if !ok {
		return base, false
	}
	merged := map[string]json.RawMessage{}
	if len(base.Metadata) > 0 {
		_ = json.Unmarshal(base.Metadata, &merged)
	}
	if raw, err := json.Marshal(poster); err == nil {
		merged["poster"] = raw
	}
	if logo != "" {
		if raw, err := json.Marshal(logo); err == nil {
			merged["logo"] = raw
		}
	}
	if background != "" {
		if raw, err := json.Marshal(background); err == nil {
			merged["background"] = raw
		}
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return base, false
	}
	base.Metadata = raw
	return base, true
}

// annotateLibraryHealth fills the Health badge field of each release from
// file_health rows matching the release download ID. Missing repos, query
// errors, or unknown IDs leave Health empty (UI renders "Unknown").
func (s *Server) annotateLibraryHealth(ctx context.Context, releases []database.LibraryRelease) {
	if s.healthRepo == nil || len(releases) == 0 {
		return
	}
	ids := make([]string, 0, len(releases))
	for _, r := range releases {
		if r.DownloadID != "" {
			ids = append(ids, r.DownloadID)
		}
	}
	statuses, err := s.healthRepo.GetHealthStatusByDownloadIDs(ctx, ids)
	if err != nil || len(statuses) == 0 {
		return
	}
	for i := range releases {
		if st, ok := statuses[releases[i].DownloadID]; ok {
			releases[i].Health = string(st)
		}
	}
}

// annotateLibraryHealthBatch fills Health for releases across many titles with
// a single lookup, replacing the previous per-title call pattern.
func (s *Server) annotateLibraryHealthBatch(ctx context.Context, titles []database.LibraryTitle) {
	if s.healthRepo == nil {
		return
	}
	var flat []*database.LibraryRelease
	ids := []string{}
	for i := range titles {
		for j := range titles[i].Releases {
			flat = append(flat, &titles[i].Releases[j])
			if titles[i].Releases[j].DownloadID != "" {
				ids = append(ids, titles[i].Releases[j].DownloadID)
			}
		}
	}
	if len(flat) == 0 || len(ids) == 0 {
		return
	}
	statuses, err := s.healthRepo.GetHealthStatusByDownloadIDs(ctx, ids)
	if err != nil || len(statuses) == 0 {
		return
	}
	for _, r := range flat {
		if st, ok := statuses[r.DownloadID]; ok {
			r.Health = string(st)
		}
	}
}

func (s *Server) handleLibraryList(c *fiber.Ctx) error {
	titles, err := s.queueRepo.LibraryTitles(c.Context())
	if err != nil {
		if isLibraryTableMissing(err) {
			return c.JSON(fiber.Map{"data": []database.LibraryTitle{}})
		}
		return c.Status(500).JSON(fiber.Map{"error": "library unavailable"})
	}
	// Allowlist filters
	indexer := strings.ToLower(strings.TrimSpace(c.Query("indexer")))
	category := strings.ToLower(strings.TrimSpace(c.Query("category")))
	instance := strings.ToLower(strings.TrimSpace(c.Query("instance")))
	sortBy := strings.ToLower(strings.TrimSpace(c.Query("sort")))
	validSorts := map[string]bool{"name": true, "imported_at": true, "size": true, "last_verified": true}
	if !validSorts[sortBy] {
		sortBy = "name"
	}
	stremioOnly := c.Query("stremio_only") == "1" || strings.EqualFold(c.Query("origin"), "stremio-only")
	if stremioOnly {
		// Completion first, against the complete unfiltered library: anchoring
		// needs the Stremio release even when an allowlist filter would hide
		// it — ARR files under an anchored title stay visible.
		// LibraryStremioComplete annotates origins itself.
		titles = LibraryStremioComplete(titles)
	}
	// Enrich once across the whole library instead of per title: the previous
	// per-title loop issued thousands of queries (~5.8s on a 4.4k-title library).
	s.annotateLibraryHealthBatch(c.Context(), titles)
	for i := range titles {
		annotateLibraryOrigin(titles[i].Releases)
	}
	s.queueRepo.EnrichLibraryTitles(c.Context(), titles)
	if indexer != "" || category != "" || instance != "" {
		for i := range titles {
			titles[i].Releases = filterReleases(titles[i].Releases, indexer, category, instance)
		}
	}
	if stremioOnly {
		// Drop titles left empty by allowlist filters (regular callers keep
		// them; anchored Stremio titles keep all releases pre-filter).
		kept := titles[:0]
		for _, t := range titles {
			if len(t.Releases) > 0 {
				kept = append(kept, t)
			}
		}
		titles = kept
	}
	if sortBy != "name" {
		for i := range titles {
			sortReleases(titles[i].Releases, sortBy)
		}
	}
	// Digest mode: compact payloads for the grid (client filters/sorts live
	// on the aggregates). Metadata blobs and release rows stay in the detail
	// endpoints.
	if c.Query("digest") == "1" {
		out := make([]libraryDigest, 0, len(titles))
		for i := range titles {
			out = append(out, libraryDigestFromTitle(titles[i]))
		}
		return c.JSON(fiber.Map{"data": out})
	}
	return c.JSON(fiber.Map{"data": titles})
}

func filterReleases(releases []database.LibraryRelease, indexer, category, instance string) []database.LibraryRelease {
	out := []database.LibraryRelease{}
	for _, r := range releases {
		if indexer != "" && strings.ToLower(r.Indexer) != indexer {
			continue
		}
		if category != "" && strings.ToLower(r.Category) != category {
			continue
		}
		if instance != "" && strings.ToLower(r.Instance) != instance {
			continue
		}
		out = append(out, r)
	}
	return out
}

func sortReleases(releases []database.LibraryRelease, sortBy string) {
	sort.Slice(releases, func(a, b int) bool {
		switch sortBy {
		case "imported_at":
			if releases[a].ImportedAt != releases[b].ImportedAt {
				return releases[a].ImportedAt > releases[b].ImportedAt
			}
		case "size":
			if releases[a].Size != releases[b].Size {
				return releases[a].Size > releases[b].Size
			}
		case "last_verified":
			if releases[a].LastVerified != releases[b].LastVerified {
				return releases[a].LastVerified > releases[b].LastVerified
			}
		}
		return strings.ToLower(releases[a].Name) < strings.ToLower(releases[b].Name)
	})
}
func (s *Server) handleLibraryDetail(c *fiber.Ctx) error {
	titles, err := s.queueRepo.LibraryTitles(c.Context())
	if err != nil {
		if isLibraryTableMissing(err) {
			return c.SendStatus(404)
		}
		return c.SendStatus(500)
	}
	for _, t := range titles {
		if t.Type == c.Params("type") && t.IMDbID == c.Params("imdbID") {
			s.annotateLibraryHealth(c.Context(), t.Releases)
			annotateLibraryOrigin(t.Releases)
			s.queueRepo.EnrichLibraryReleases(c.Context(), t.Releases)
			return c.JSON(fiber.Map{"data": t})
		}
	}
	return c.SendStatus(404)
}
func (s *Server) handleLibraryRecent(c *fiber.Ctx) error {
	if s.configManager == nil {
		return c.SendStatus(503)
	}
	stremioOnly := c.Query("stremio_only") == "1" || strings.EqualFold(c.Query("origin"), "stremio-only")
	cfg := s.configManager.GetConfig()
	items, err := s.queueRepo.GetCachedStremioQueueItems(c.Context(), false, cfg.Stremio.NzbTTLHours)
	if err != nil {
		return c.SendStatus(500)
	}
	refs, err := s.queueRepo.LibraryReleases(c.Context())
	if err != nil {
		if isLibraryTableMissing(err) {
			refs = []database.LibraryRelease{}
		} else {
			return c.SendStatus(500)
		}
	}
	out := []database.LibraryRelease{}
	for _, item := range items {
		// Recent covers identifiable imports only: Stremio plays and ARR
		// post-processing imports. Unidentifiable uploads cannot be titled
		// and are saved via the "all" auto-save scope instead.
		if classifyLibraryItem(item) == librarySourceOther {
			continue
		}
		t, season, episode, ok := libraryTitleForAutosave(item)
		if !ok {
			continue
		}
		v := database.LibraryRelease{QueueID: item.ID, Name: t.Name, Type: t.Type, IMDbID: t.IMDbID, Season: season, Episode: episode}
		if item.DownloadID != nil {
			v.DownloadID = *item.DownloadID
		}
		for _, ref := range refs {
			if ref.QueueID == item.ID {
				v = ref
				break
			}
		}
		out = append(out, v)
	}
	s.annotateLibraryHealth(c.Context(), out)
	annotateLibraryOrigin(out)
	if stremioOnly {
		// Recent tab is a release-level policy (distinct from saved-title
		// completion): keep all Stremio-origin temporary releases, including
		// unverified episodes. ARR/manual recents are excluded. Temporary
		// items have no completion grouping — nothing to merge under.
		kept := out[:0]
		for _, r := range out {
			if r.Origin != librarySourceStremio {
				continue
			}
			kept = append(kept, r)
		}
		out = kept
		if out == nil {
			out = []database.LibraryRelease{}
		}
	}
	s.queueRepo.EnrichLibraryReleases(c.Context(), out)
	return c.JSON(fiber.Map{"data": out})
}
func (s *Server) handleLibrarySave(c *fiber.Ctx) error {
	var req struct {
		QueueID int64 `json:"queue_id"`
	}
	if c.BodyParser(&req) != nil || req.QueueID <= 0 {
		return c.SendStatus(400)
	}
	item, err := s.queueRepo.GetQueueItem(c.Context(), req.QueueID)
	if err != nil || item == nil {
		return c.SendStatus(404)
	}
	t, mSeason, mEpisode, ok := libraryTitleForAutosave(item)
	if !ok {
		return c.Status(422).JSON(fiber.Map{"error": "release has no identifiable movie/series title"})
	}
	if strings.HasPrefix(t.IMDbID, "tt") {
		t = enrichLibraryTitle(c.Context(), t)
	} else {
		t = enrichLibraryTitleBySearch(c.Context(), t, s.configManager.GetConfig().GetUserAgent())
	}
	database.LibraryAssetMu.Lock()
	defer database.LibraryAssetMu.Unlock()
	release, err := s.queueRepo.SaveLibraryRelease(c.Context(), req.QueueID, t, mSeason, mEpisode)
	if err != nil {
		return c.Status(409).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": release})
}
func (s *Server) handleLibraryUnsave(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return c.SendStatus(400)
	}
	if s.configManager == nil {
		return c.SendStatus(503)
	}
	grace := time.Duration(s.configManager.GetConfig().Stremio.NzbTTLHours) * time.Hour
	release, err := s.queueRepo.UnsaveLibraryRelease(c.Context(), id, grace)
	if err != nil {
		return c.SendStatus(404)
	}
	return c.JSON(fiber.Map{"data": release})
}
func (s *Server) libraryAddonAuth(c *fiber.Ctx) bool {
	return s.configManager != nil && isStremioEnabled(s.configManager.GetConfig()) && s.validateDownloadKey(c.Context(), c.Params("key"))
}
func (s *Server) handleLibraryCatalog(c *fiber.Ctx) error {
	if !s.libraryAddonAuth(c) {
		return c.SendStatus(401)
	}
	if c.Params("catalog") != "altmount-saved" || (c.Params("type") != "movie" && c.Params("type") != "series") {
		return c.JSON(fiber.Map{"metas": []any{}})
	}
	values, err := url.ParseQuery(c.Params("extra"))
	if err != nil {
		values = url.Values{}
	}
	for k, vs := range c.Queries() {
		if _, ok := values[k]; !ok {
			values[k] = []string{vs}
		}
	}
	skip := 0
	if v := values.Get("skip"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			skip = n
		}
	}
	search := strings.ToLower(strings.TrimSpace(values.Get("search")))
	titles, err := s.queueRepo.LibraryTitles(c.Context())
	if err != nil {
		return c.JSON(fiber.Map{"metas": []any{}})
	}
	metas := []json.RawMessage{}
	matched := 0
	for _, t := range titles {
		if t.Type != c.Params("type") || !strings.Contains(strings.ToLower(t.Name), search) {
			continue
		}
		if matched >= skip && len(metas) < 100 {
			metas = append(metas, t.Metadata)
		}
		matched++
	}
	return c.JSON(fiber.Map{"metas": metas})
}
func (s *Server) handleLibraryMeta(c *fiber.Ctx) error {
	if !s.libraryAddonAuth(c) {
		return c.SendStatus(401)
	}
	titles, err := s.queueRepo.LibraryTitles(c.Context())
	if err != nil {
		return c.JSON(fiber.Map{"meta": nil})
	}
	for _, t := range titles {
		if t.Type == c.Params("type") && t.IMDbID == c.Params("id") {
			return c.JSON(fiber.Map{"meta": t.Metadata})
		}
	}
	return c.JSON(fiber.Map{"meta": nil})
}
