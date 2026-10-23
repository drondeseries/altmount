package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LibraryAssetMu serializes saved-reference changes and Stremio/queue asset deletion
// across repository instances in this process. AltMount runs one writer process.
var LibraryAssetMu sync.Mutex

type LibraryRelease struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	IMDbID    string `json:"imdb_id"`
	QueueID   int64  `json:"queue_id"`
	Name      string `json:"name"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Saved     bool   `json:"saved"`
	ExpiresAt int64  `json:"expires_at"`
	// Health is the worst file_health status across files imported under this
	// release's download ID ("healthy", "degraded", "corrupted", "pending",
	// "checking", "repair_triggered", or "" when no health data exists yet).
	Health string `json:"health,omitempty"`
	// Size is the release size in bytes when known (queue snapshots carry it).
	Size int64 `json:"size,omitempty"`
	// Location is the ARR-managed library/symlink path when known.
	Location string `json:"location,omitempty"`
	// TmdbID is the TMDB movie ID from ARR metadata when known.
	TmdbID int64 `json:"tmdb_id,omitempty"`
	// Enriched from import_history / file_health (not stored in library_releases).
	Indexer      string `json:"indexer,omitempty"`
	Instance     string `json:"instance,omitempty"`
	Category     string `json:"category,omitempty"`
	ImportedAt   int64  `json:"imported_at,omitempty"`
	SceneName    string `json:"scene_name,omitempty"`
	LastVerified int64  `json:"last_verified,omitempty"`
	RetryCount   int    `json:"retry_count,omitempty"`
	StreamFails  int    `json:"streaming_failures,omitempty"`
	// Source is "queue" (import-queue snapshot) or "health" (file_health
	// backfill: queue row and NZB are long gone, media served from FilePath).
	Source string `json:"source"`
	// Origin is stremio, arrs, or manual (computed at read time, not stored).
	Origin         string          `json:"origin,omitempty"`
	FileHealthID   *int64          `json:"-"`
	HealthMetadata *string         `json:"-"`
	FilePath       string          `json:"-"`
	NzbPath        string          `json:"-"`
	StoragePath    string          `json:"-"`
	DownloadID     string          `json:"-"`
	Snapshot       ImportQueueItem `json:"-"`
}
type LibraryTitle struct {
	Type     string           `json:"type"`
	IMDbID   string           `json:"imdb_id"`
	Name     string           `json:"name"`
	Metadata json.RawMessage  `json:"metadata"`
	Releases []LibraryRelease `json:"releases"`
}

// isLibrarySchemaMissing reports a missing table or column so readers keep
// working on pre-migration databases (042 before 043).
func isLibrarySchemaMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "no such column") ||
		strings.Contains(msg, "does not exist") || strings.Contains(msg, "undefined table") ||
		strings.Contains(msg, "unknown table") || strings.Contains(msg, "unknown column")
}

func (r *Repository) LibraryReleases(ctx context.Context) ([]LibraryRelease, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT library_releases.id,library_releases.type,library_releases.imdb_id,library_releases.queue_id,library_releases.name,library_releases.season,library_releases.episode,library_releases.saved,library_releases.expires_at,library_releases.nzb_path,library_releases.storage_path,library_releases.download_id,library_releases.snapshot,library_releases.source,library_releases.file_health_id,library_releases.file_path,fh.metadata AS health_metadata FROM library_releases LEFT JOIN file_health fh ON fh.id = library_releases.file_health_id ORDER BY library_releases.id DESC`)
	if err != nil {
		if !isLibrarySchemaMissing(err) {
			return nil, err
		}
		return r.legacyLibraryReleases(ctx)
	}
	defer rows.Close()
	out := []LibraryRelease{}
	for rows.Next() {
		var v LibraryRelease
		var saved int
		var snapshot string
		var source sql.NullString
		var healthID sql.NullInt64
		var filePath, healthMeta sql.NullString
		if err := rows.Scan(&v.ID, &v.Type, &v.IMDbID, &v.QueueID, &v.Name, &v.Season, &v.Episode, &saved, &v.ExpiresAt, &v.NzbPath, &v.StoragePath, &v.DownloadID, &snapshot, &source, &healthID, &filePath, &healthMeta); err != nil {
			rows.Close()
			if isLibrarySchemaMissing(err) {
				return r.legacyLibraryReleases(ctx)
			}
			return nil, err
		}
		v.Saved = saved == 1
		v.Source = source.String
		if v.Source == "" {
			v.Source = "queue"
		}
		if healthID.Valid {
			id := healthID.Int64
			v.FileHealthID = &id
		}
		v.FilePath = filePath.String
		if healthMeta.Valid {
			meta := healthMeta.String
			v.HealthMetadata = &meta
		}
		if err := json.Unmarshal([]byte(snapshot), &v.Snapshot); err != nil {
			return nil, err
		}
		if v.Snapshot.FileSize != nil {
			v.Size = *v.Snapshot.FileSize
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// legacyLibraryReleases reads the pre-043 queue-only shape.
func (r *Repository) legacyLibraryReleases(ctx context.Context) ([]LibraryRelease, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,type,imdb_id,queue_id,name,season,episode,saved,expires_at,nzb_path,storage_path,download_id,snapshot FROM library_releases ORDER BY id DESC`)
	if err != nil {
		if isLibrarySchemaMissing(err) {
			return []LibraryRelease{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := []LibraryRelease{}
	for rows.Next() {
		var v LibraryRelease
		var saved int
		var snapshot string
		if err := rows.Scan(&v.ID, &v.Type, &v.IMDbID, &v.QueueID, &v.Name, &v.Season, &v.Episode, &saved, &v.ExpiresAt, &v.NzbPath, &v.StoragePath, &v.DownloadID, &snapshot); err != nil {
			return nil, err
		}
		v.Saved = saved == 1
		v.Source = "queue"
		if err := json.Unmarshal([]byte(snapshot), &v.Snapshot); err != nil {
			return nil, err
		}
		if v.Snapshot.FileSize != nil {
			v.Size = *v.Snapshot.FileSize
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *Repository) LibraryTitles(ctx context.Context) ([]LibraryTitle, error) {
	releases, err := r.LibraryReleases(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT type,imdb_id,name,metadata FROM library_titles ORDER BY name,imdb_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibraryTitle{}
	// Index saved releases by title in one pass. The previous nested loop was
	// O(titles x releases) (~186M comparisons on a large library) and dominated
	// the whole request at ~4.4s. Appending in query order preserves the prior
	// per-title ordering (library_releases.id DESC).
	byTitle := make(map[string][]LibraryRelease, len(releases))
	for _, v := range releases {
		if !v.Saved {
			continue
		}
		key := v.Type + "\x00" + v.IMDbID
		byTitle[key] = append(byTitle[key], v)
	}
	for rows.Next() {
		var t LibraryTitle
		var raw string
		if err := rows.Scan(&t.Type, &t.IMDbID, &t.Name, &raw); err != nil {
			return nil, err
		}
		t.Metadata = json.RawMessage(raw)
		t.Releases = byTitle[t.Type+"\x00"+t.IMDbID]
		if t.Releases == nil {
			t.Releases = []LibraryRelease{}
		}
		if len(t.Releases) > 0 {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// SaveLibraryRelease must be called under LibraryAssetMu, after metadata enrichment.
// The item is re-read inside the gate, so a stale recent-list response cannot save deleted assets.
func (r *Repository) SaveLibraryRelease(ctx context.Context, id int64, t LibraryTitle, season, episode int) (*LibraryRelease, error) {
	item, err := r.GetQueueItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil || item.Status != QueueStatusCompleted || item.StoragePath == nil || *item.StoragePath == "" {
		return nil, fmt.Errorf("only completed imports with storage can be saved")
	}
	snapshot, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	// Movies never carry season/episode: a nonzero value here is always a
	// producer bug, and the UI renders S/E badges for series only.
	if t.Type == "movie" {
		season, episode = 0, 0
	}
	err = r.WithTransaction(ctx, func(tx *Repository) error {
		_, err := tx.db.ExecContext(ctx, `INSERT INTO library_titles(type,imdb_id,name,metadata) VALUES(?,?,?,?) ON CONFLICT(type,imdb_id) DO NOTHING`, t.Type, t.IMDbID, t.Name, string(t.Metadata))
		if err != nil {
			return err
		}
		_, err = tx.db.ExecContext(ctx, `INSERT INTO library_releases(type,imdb_id,queue_id,name,season,episode,nzb_path,storage_path,download_id,snapshot) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(queue_id) DO UPDATE SET saved=1,expires_at=0`, t.Type, t.IMDbID, id, filepath.Base(item.NzbPath), season, episode, item.NzbPath, *item.StoragePath, *item.DownloadID, string(snapshot))
		return err
	})
	if err != nil {
		return nil, err
	}
	releases, err := r.LibraryReleases(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range releases {
		if v.QueueID == id {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("saved release not found")
}

// ImportHealthRelease pins a file_health record into the saved library.
// Health rows have no queue entry left (queue cleared, NZB deleted), so they
// are keyed by a negative queue_id sentinel derived from the file_health ID:
// unique, and never colliding with real queue IDs. Media is served from
// filePath; storage_path mirrors it so asset-overlap protection applies.
func (r *Repository) ImportHealthRelease(ctx context.Context, healthID int64, filePath string, t LibraryTitle, season, episode int, downloadID, nzbPath, releaseName string) (*LibraryRelease, error) {
	if healthID <= 0 || strings.TrimSpace(filePath) == "" {
		return nil, fmt.Errorf("health import needs a file_health ID and path")
	}
	if t.Type == "movie" {
		season, episode = 0, 0
	}
	if nzbPath == "" {
		nzbPath = filePath
	}
	name := releaseName
	if name == "" {
		name = t.Name
	}
	if name == "" {
		name = filepath.Base(filePath)
	}
	err := r.WithTransaction(ctx, func(tx *Repository) error {
		if _, err := tx.db.ExecContext(ctx, `INSERT INTO library_titles(type,imdb_id,name,metadata) VALUES(?,?,?,?) ON CONFLICT(type,imdb_id) DO NOTHING`, t.Type, t.IMDbID, t.Name, string(t.Metadata)); err != nil {
			return err
		}
		_, err := tx.db.ExecContext(ctx, `INSERT INTO library_releases(type,imdb_id,queue_id,name,season,episode,nzb_path,storage_path,download_id,snapshot,source,file_health_id,file_path) VALUES(?,?,?,?,?,?,?,?,?,?,'health',?,?) ON CONFLICT(queue_id) DO UPDATE SET saved=1,expires_at=0`, t.Type, t.IMDbID, -healthID, name, season, episode, nzbPath, filePath, downloadID, "{}", healthID, filePath)
		return err
	})
	if err != nil {
		return nil, err
	}
	releases, err := r.LibraryReleases(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range releases {
		if v.FileHealthID != nil && *v.FileHealthID == healthID {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("imported release not found")
}
func (r *Repository) UnsaveLibraryRelease(ctx context.Context, id int64, grace time.Duration) (*LibraryRelease, error) {
	LibraryAssetMu.Lock()
	defer LibraryAssetMu.Unlock()
	if grace < 24*time.Hour {
		grace = 24 * time.Hour
	}
	_, err := r.db.ExecContext(ctx, `UPDATE library_releases SET saved=0,expires_at=? WHERE id=? AND saved=1`, time.Now().Add(grace).Unix(), id)
	if err != nil {
		return nil, err
	}
	releases, err := r.LibraryReleases(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range releases {
		if v.ID == id {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("release not found")
}
func pathsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

// LibraryAssetProtected fails closed on DB errors. Caller holds LibraryAssetMu through deletion.
func (r *Repository) LibraryAssetProtected(ctx context.Context, nzb, storage string) bool {
	releases, err := r.LibraryReleases(ctx)
	if err != nil {
		return true
	}
	for _, v := range releases {
		if (v.Saved || v.ExpiresAt > time.Now().Unix()) && ((nzb != "" && nzb == v.NzbPath) || pathsOverlap(storage, v.StoragePath)) {
			return true
		}
	}
	return false
}

// sceneNameFromMetadata extracts a scene_name from raw import_history
// metadata JSON. Invalid or missing JSON yields "".
func sceneNameFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	if name, ok := m["scene_name"].(string); ok {
		return strings.TrimSpace(name)
	}
	return ""
}

// arrHealthFields extracts display-safe fields from ARR webhook metadata:
// the managing instance name, scene name, and TMDB movie ID.
func arrHealthFields(raw string) (instance, sceneName string, tmdbID int64) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", 0
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", "", 0
	}
	if name, ok := m["instanceName"].(string); ok {
		instance = strings.TrimSpace(name)
	}
	obj, _ := m["movie"].(map[string]any)
	if obj == nil {
		obj, _ = m["series"].(map[string]any)
	}
	if obj != nil {
		for _, key := range []string{"tmdbId", "tmdb_id"} {
			if id, ok := obj[key].(float64); ok && id > 0 {
				tmdbID = int64(id)
				break
			}
		}
	}
	// Scene names live on the file objects, not the movie/series objects.
	for _, key := range []string{"movieFile", "episodeFile"} {
		if fobj, ok := m[key].(map[string]any); ok {
			if scene, ok := fobj["sceneName"].(string); ok && strings.TrimSpace(scene) != "" {
				sceneName = strings.TrimSpace(scene)
				break
			}
		}
	}
	return instance, sceneName, tmdbID
}

func arrInstanceName(raw string) string {
	instance, _, _ := arrHealthFields(raw)
	return instance
}

func arrSceneName(raw string) string {
	_, scene, _ := arrHealthFields(raw)
	return scene
}

func arrTMDBID(raw string) int64 {
	_, _, id := arrHealthFields(raw)
	return id
}

// parseLibraryTime parses SQLite ("2006-01-02 15:04:05") and Postgres
// (RFC3339) timestamps into unix seconds. Unparseable values yield 0.
func parseLibraryTime(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// libraryEnrichChunk bounds IN-clause size; SQLite's default variable limit is
// 999, so chunking keeps large libraries working without touching PRAGMAs.
const libraryEnrichChunk = 900

// EnrichLibraryTitles enriches every release across all titles in a single
// batched pass. Calling EnrichLibraryReleases once per title turned a large
// library into thousands of queries; this flattens first, then writes results
// back into the caller's slices.
func (r *Repository) EnrichLibraryTitles(ctx context.Context, titles []LibraryTitle) {
	total := 0
	for i := range titles {
		total += len(titles[i].Releases)
	}
	if total == 0 {
		return
	}
	flat := make([]LibraryRelease, 0, total)
	for i := range titles {
		flat = append(flat, titles[i].Releases...)
	}
	r.EnrichLibraryReleases(ctx, flat)
	// EnrichLibraryReleases mutates a copy; copy the enriched values back by
	// position so callers see the same ordering they passed in.
	idx := 0
	for i := range titles {
		copy(titles[i].Releases, flat[idx:idx+len(titles[i].Releases)])
		idx += len(titles[i].Releases)
	}
}

// EnrichLibraryReleases fills import_history fields (indexer, instance,
// category, completed_at, scene_name) and file_health fields (last_checked,
// retry_count, streaming_failure_count) for each release via download_id /
// file_health ID lookups. Missing rows leave fields empty; errors are
// non-fatal (best-effort display enrichment).
func (r *Repository) EnrichLibraryReleases(ctx context.Context, releases []LibraryRelease) {
	if len(releases) == 0 {
		return
	}
	// 1. import_history by download_id.
	byDownload := map[string]struct {
		indexer, instance, category string
		completedAt                 int64
		sceneName                   string
		size                        int64
	}{}
	seenDownload := map[string]struct{}{}
	var downloads []string
	for _, rel := range releases {
		if rel.DownloadID == "" {
			continue
		}
		if _, dup := seenDownload[rel.DownloadID]; dup {
			continue
		}
		seenDownload[rel.DownloadID] = struct{}{}
		downloads = append(downloads, rel.DownloadID)
	}
	for start := 0; start < len(downloads); start += libraryEnrichChunk {
		end := min(start+libraryEnrichChunk, len(downloads))
		chunk := downloads[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, d := range chunk {
			placeholders[i] = "?"
			args[i] = d
		}
		fullQuery := fmt.Sprintf(
			`SELECT download_id, COALESCE(NULLIF(indexer,''),NULLIF(instance_name,'')) AS src,
			        COALESCE(NULLIF(instance_name,''),'') AS inst,
			        COALESCE(NULLIF(category,''),'') AS cat,
			        completed_at AS done,
			        metadata AS meta,
			        COALESCE(file_size,0) AS sz
			 FROM import_history
			 WHERE download_id IN (%s)
			 ORDER BY completed_at DESC`, strings.Join(placeholders, ","))
		// Older databases lack instance_name: fall back to indexer/category only.
		reducedQuery := fmt.Sprintf(
			`SELECT download_id, COALESCE(NULLIF(indexer,''),'') AS src,
			        '' AS inst,
			        COALESCE(NULLIF(category,''),'') AS cat,
			        completed_at AS done,
			        metadata AS meta,
			        COALESCE(file_size,0) AS sz
			 FROM import_history
			 WHERE download_id IN (%s)
			 ORDER BY completed_at DESC`, strings.Join(placeholders, ","))
		rows, err := r.db.QueryContext(ctx, fullQuery, args...)
		if err != nil && isLibrarySchemaMissing(err) {
			rows, err = r.db.QueryContext(ctx, reducedQuery, args...)
		}
		if err == nil {
			for rows.Next() {
				var dl, indexer, instance, category string
				var doneRaw, metaRaw sql.NullString
				var size int64
				if err := rows.Scan(&dl, &indexer, &instance, &category, &doneRaw, &metaRaw, &size); err == nil {
					if _, exists := byDownload[dl]; !exists {
						byDownload[dl] = struct {
							indexer, instance, category string
							completedAt                 int64
							sceneName                   string
							size                        int64
						}{indexer, instance, category, parseLibraryTime(doneRaw.String), sceneNameFromMetadata(metaRaw.String), size}
					}
				}
			}
			rows.Close()
		}
	}
	// 2. file_health by file_health_id.
	byHealth := map[int64]struct {
		lastChecked int64
		retryCount  int
		streamFails int
		instance    string
		sceneName   string
		tmdbID      int64
		location    string
	}{}
	seenHealth := map[int64]struct{}{}
	var healthIDs []int64
	for _, rel := range releases {
		if rel.FileHealthID == nil {
			continue
		}
		if _, dup := seenHealth[*rel.FileHealthID]; dup {
			continue
		}
		seenHealth[*rel.FileHealthID] = struct{}{}
		healthIDs = append(healthIDs, *rel.FileHealthID)
	}
	for start := 0; start < len(healthIDs); start += libraryEnrichChunk {
		end := min(start+libraryEnrichChunk, len(healthIDs))
		chunk := healthIDs[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := r.db.QueryContext(ctx, fmt.Sprintf(
			`SELECT id, last_checked AS checked,
			        COALESCE(retry_count,0) AS retries,
			        COALESCE(streaming_failure_count,0) AS fails,
			        metadata AS meta,
			        library_path AS libpath
			 FROM file_health WHERE id IN (%s)`, strings.Join(placeholders, ",")), args...)
		if err == nil {
			for rows.Next() {
				var id int64
				var checked, metaRaw, libPath sql.NullString
				var retries, fails int
				if err := rows.Scan(&id, &checked, &retries, &fails, &metaRaw, &libPath); err == nil {
					byHealth[id] = struct {
						lastChecked int64
						retryCount  int
						streamFails int
						instance    string
						sceneName   string
						tmdbID      int64
						location    string
					}{parseLibraryTime(checked.String), retries, fails,
						arrInstanceName(metaRaw.String), arrSceneName(metaRaw.String),
						arrTMDBID(metaRaw.String), libPath.String}
				}
			}
			rows.Close()
		}
	}
	for i := range releases {
		if h, ok := byDownload[releases[i].DownloadID]; ok {
			releases[i].Indexer = h.indexer
			releases[i].Instance = h.instance
			releases[i].Category = h.category
			releases[i].ImportedAt = h.completedAt
			releases[i].SceneName = h.sceneName
			if releases[i].Size == 0 {
				releases[i].Size = h.size
			}
		}
		if releases[i].FileHealthID != nil {
			if f, ok := byHealth[*releases[i].FileHealthID]; ok {
				releases[i].LastVerified = f.lastChecked
				releases[i].RetryCount = f.retryCount
				releases[i].StreamFails = f.streamFails
				if releases[i].Instance == "" {
					releases[i].Instance = f.instance
				}
				if releases[i].SceneName == "" {
					releases[i].SceneName = f.sceneName
				}
				if releases[i].TmdbID == 0 {
					releases[i].TmdbID = f.tmdbID
				}
				if releases[i].Location == "" {
					releases[i].Location = f.location
				}
			}
		}
	}
}

// DeleteLibraryRelease removes a library release row (and its title when no
// saved releases reference it anymore).
func (r *Repository) DeleteLibraryRelease(ctx context.Context, id int64) error {
	LibraryAssetMu.Lock()
	defer LibraryAssetMu.Unlock()
	var mediaType, imdbID string
	_ = r.db.QueryRowContext(ctx, `SELECT type, imdb_id FROM library_releases WHERE id=?`, id).Scan(&mediaType, &imdbID)
	if _, err := r.db.ExecContext(ctx, `DELETE FROM library_releases WHERE id=?`, id); err != nil {
		return err
	}
	if mediaType != "" {
		_, _ = r.db.ExecContext(ctx, `DELETE FROM library_titles WHERE type=? AND imdb_id=? AND NOT EXISTS (SELECT 1 FROM library_releases WHERE type=? AND imdb_id=? AND saved=1)`, mediaType, imdbID, mediaType, imdbID)
	}
	return nil
}

// UpdateLibraryTitleMetadata rewrites a title's display name and stored
// metadata blob in place (same type+imdb_id). Used by the poster-refresh pass
// for real tt… titles whose metadata is thin/absent; callers hold
// LibraryAssetMu, matching SaveLibraryRelease and friends.
func (r *Repository) UpdateLibraryTitleMetadata(ctx context.Context, mediaType, imdbID, name string, metadata json.RawMessage) error {
	if mediaType == "" || imdbID == "" {
		return fmt.Errorf("media type and imdb id are required")
	}
	_, err := r.db.ExecContext(ctx, `UPDATE library_titles SET name=?, metadata=? WHERE type=? AND imdb_id=?`, name, string(metadata), mediaType, imdbID)
	return err
}

// RelinkLibraryTitle moves all releases from one title identity to another
// (e.g. synthetic altm-movie-tmdb-584 to resolved tt0322259) when a later
// metadata lookup succeeds. Release display names are preserved; only the
// title identity, title name, and title metadata change.
func (r *Repository) RelinkLibraryTitle(ctx context.Context, mediaType, oldIMDbID string, resolved LibraryTitle) error {
	if mediaType == "" || oldIMDbID == "" || resolved.IMDbID == "" || oldIMDbID == resolved.IMDbID {
		return fmt.Errorf("invalid title relink")
	}
	return r.WithTransaction(ctx, func(tx *Repository) error {
		if _, err := tx.db.ExecContext(ctx, `INSERT INTO library_titles(type,imdb_id,name,metadata) VALUES(?,?,?,?) ON CONFLICT(type,imdb_id) DO UPDATE SET name=excluded.name, metadata=excluded.metadata`, resolved.Type, resolved.IMDbID, resolved.Name, string(resolved.Metadata)); err != nil {
			return err
		}
		if _, err := tx.db.ExecContext(ctx, `UPDATE library_releases SET imdb_id=? WHERE type=? AND imdb_id=?`, resolved.IMDbID, mediaType, oldIMDbID); err != nil {
			return err
		}
		_, err := tx.db.ExecContext(ctx, `DELETE FROM library_titles WHERE type=? AND imdb_id=? AND NOT EXISTS (SELECT 1 FROM library_releases WHERE type=? AND imdb_id=?)`, mediaType, oldIMDbID, mediaType, oldIMDbID)
		return err
	})
}

func (r *Repository) DeleteExpiredLibraryReference(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM library_releases WHERE id=? AND saved=0 AND expires_at<=?`, id, time.Now().Unix())
	return err
}

// SplitLibraryReleases reattaches only the given releases of one title
// identity to a different title inside a single transaction. The split audit
// uses it to detach releases whose parsed year conflicts with the title's
// canonical year (e.g. "Africa (2001)" merged under "Africa (2013)") into a
// year-keyed synthetic altm-* anchor; unlike RelinkLibraryTitle it moves a
// subset, so the merged title keeps its majority releases. The target row is
// upserted with DO NOTHING so a repeated audit never rewrites an existing
// synthetic identity.
func (r *Repository) SplitLibraryReleases(ctx context.Context, mediaType, fromIMDbID string, to LibraryTitle, releaseIDs []int64) error {
	if mediaType == "" || fromIMDbID == "" || to.IMDbID == "" || len(releaseIDs) == 0 {
		return fmt.Errorf("split needs a source identity, a target identity, and at least one release")
	}
	return r.WithTransaction(ctx, func(tx *Repository) error {
		if _, err := tx.db.ExecContext(ctx, `INSERT INTO library_titles(type,imdb_id,name,metadata) VALUES(?,?,?,?) ON CONFLICT(type,imdb_id) DO NOTHING`, to.Type, to.IMDbID, to.Name, string(to.Metadata)); err != nil {
			return err
		}
		args := make([]any, 0, len(releaseIDs)+2)
		args = append(args, to.IMDbID, mediaType, fromIMDbID)
		placeholders := make([]string, 0, len(releaseIDs))
		for _, id := range releaseIDs {
			args = append(args, id)
			placeholders = append(placeholders, "?")
		}
		_, err := tx.db.ExecContext(ctx, `UPDATE library_releases SET imdb_id=? WHERE type=? AND imdb_id=? AND id IN (`+strings.Join(placeholders, ",")+`)`, args...)
		return err
	})
}
