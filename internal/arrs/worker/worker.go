package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/javi11/altmount/internal/arrs/clients"
	"github.com/javi11/altmount/internal/arrs/instances"
	"github.com/javi11/altmount/internal/arrs/model"
	"github.com/javi11/altmount/internal/config"
	"github.com/javi11/altmount/internal/database"
	"golift.io/starr"
)

type Worker struct {
	configGetter config.ConfigGetter
	instances    *instances.Manager
	clients      *clients.Manager
	repo         *database.Repository

	// Queue cleanup worker state
	workerCtx     context.Context
	workerCancel  context.CancelFunc
	workerWg      sync.WaitGroup
	workerMu      sync.Mutex
	workerRunning bool

	// firstSeen tracks when a failed import item was first seen
	// key: instanceName|queueID
	firstSeen   map[string]time.Time
	firstSeenMu sync.RWMutex

	// Harvester state
	lastHarvest   time.Time
}

func NewWorker(configGetter config.ConfigGetter, instances *instances.Manager, clients *clients.Manager, repo *database.Repository) *Worker {
	return &Worker{
		configGetter: configGetter,
		instances:    instances,
		clients:      clients,
		repo:         repo,
		firstSeen:    make(map[string]time.Time),
	}
}

// Start starts the queue cleanup and harvesting workers
func (w *Worker) Start(ctx context.Context) error {
	w.workerMu.Lock()
	defer w.workerMu.Unlock()

	if w.workerRunning {
		return nil
	}

	cfg := w.configGetter()

	// ARRs must be enabled
	if cfg.Arrs.Enabled == nil || !*cfg.Arrs.Enabled {
		slog.InfoContext(ctx, "ARR workers disabled (ARRs disabled)")
		return nil
	}

	w.workerCtx, w.workerCancel = context.WithCancel(ctx)
	w.workerRunning = true

	// 1. Start Queue Cleanup Worker
	cleanupEnabled := true
	if cfg.Arrs.QueueCleanupEnabled != nil {
		cleanupEnabled = *cfg.Arrs.QueueCleanupEnabled
	}

	if cleanupEnabled {
		interval := time.Duration(cfg.Arrs.QueueCleanupIntervalSeconds) * time.Second
		if interval <= 0 {
			interval = 10 * time.Second
		}

		w.workerWg.Add(1)
		go w.runWorker(interval)
		slog.InfoContext(ctx, "ARR queue cleanup worker started",
			"interval_seconds", cfg.Arrs.QueueCleanupIntervalSeconds)
	}

	// 2. Start DownloadID Harvester Worker
	harvestEnabled := true
	if cfg.Arrs.HarvestDownloadIDsEnabled != nil {
		harvestEnabled = *cfg.Arrs.HarvestDownloadIDsEnabled
	}

	if harvestEnabled {
		interval := time.Duration(cfg.Arrs.HarvestDownloadIDsIntervalHours) * time.Hour
		if interval <= 0 {
			interval = 24 * time.Hour
		}

		w.workerWg.Add(1)
		go w.runHarvester(interval)
		slog.InfoContext(ctx, "ARR download ID harvester worker started",
			"interval_hours", cfg.Arrs.HarvestDownloadIDsIntervalHours)
	}

	return nil
}

// Stop stops the queue cleanup worker
func (w *Worker) Stop(ctx context.Context) {
	w.workerMu.Lock()
	defer w.workerMu.Unlock()

	if !w.workerRunning {
		return
	}

	w.workerCancel()
	w.workerWg.Wait()
	w.workerRunning = false
	slog.InfoContext(ctx, "ARR queue cleanup worker stopped")
}

func (w *Worker) runWorker(interval time.Duration) {
	defer w.workerWg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial delay before first run
	select {
	case <-time.After(30 * time.Second):
	case <-w.workerCtx.Done():
		return
	}

	// Run initial cleanup
	w.safeCleanup()

	for {
		select {
		case <-ticker.C:
			w.safeCleanup()
		case <-w.workerCtx.Done():
			return
		}
	}
}

func (w *Worker) safeCleanup() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic in queue cleanup", "panic", r)
		}
	}()
	if err := w.CleanupQueue(w.workerCtx); err != nil {
		slog.Error("Queue cleanup failed", "error", err)
	}
}

func (w *Worker) runHarvester(interval time.Duration) {
	defer w.workerWg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial run after a short delay
	select {
	case <-time.After(1 * time.Minute):
		w.safeHarvest()
	case <-w.workerCtx.Done():
		return
	}

	for {
		select {
		case <-ticker.C:
			w.safeHarvest()
		case <-w.workerCtx.Done():
			return
		}
	}
}

func (w *Worker) safeHarvest() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Panic in DownloadID harvester", "panic", r)
		}
	}()
	if err := w.HarvestDownloadIDs(w.workerCtx); err != nil {
		slog.Error("DownloadID harvesting failed", "error", err)
	}
}

// HarvestDownloadIDs scans library items in AltMount's history that are missing GUIDs
// and attempts to "harvest" them from ARR history for proactive tracking.
func (w *Worker) HarvestDownloadIDs(ctx context.Context) error {
	slog.InfoContext(ctx, "Starting proactive DownloadID harvesting cycle for legacy library items")

	// 1. Get history items missing download_id
	items, err := w.repo.GetHistoryMissingDownloadID(ctx, 100) // Process in chunks of 100
	if err != nil {
		return fmt.Errorf("failed to get history items for harvesting: %w", err)
	}

	if len(items) == 0 {
		slog.InfoContext(ctx, "No legacy items found missing DownloadID")
		return nil
	}

	slog.InfoContext(ctx, "Found items missing DownloadID, attempting to harvest from ARRs", "count", len(items))

	instances := w.instances.GetAllInstances()
	harvestedCount := 0

	for _, item := range items {
		found := false
		for _, instance := range instances {
			if !instance.Enabled {
				continue
			}

			var downloadID string
			switch instance.Type {
			case "radarr", "whisparr":
				downloadID = w.harvestRadarrDownloadID(ctx, instance, item)
			case "sonarr":
				downloadID = w.harvestSonarrDownloadID(ctx, instance, item)
			}

			if downloadID != "" {
				slog.InfoContext(ctx, "Harvested DownloadID for legacy item",
					"path", item.VirtualPath, "download_id", downloadID, "instance", instance.Name)
				if err := w.repo.UpdateDownloadIDByPath(ctx, item.VirtualPath, downloadID); err == nil {
					harvestedCount++
					found = true
				}
				break
			}
		}

		if !found {
			slog.DebugContext(ctx, "Could not find DownloadID in any ARR history for item", "path", item.VirtualPath)
		}
	}

	slog.InfoContext(ctx, "Finished DownloadID harvesting cycle", "harvested", harvestedCount, "total_processed", len(items))
	return nil
}

func (w *Worker) harvestRadarrDownloadID(ctx context.Context, instance *model.ConfigInstance, item *database.ImportHistory) string {
	client, err := w.clients.GetOrCreateRadarrClient(instance.Name, instance.URL, instance.APIKey)
	if err != nil {
		return ""
	}

	// Fetch history using filename match if possible
	req := &starr.PageReq{PageSize: 50, SortKey: "date", SortDir: starr.SortDescend}
	// We search history for the filename
	history, err := client.GetHistoryPageContext(ctx, req)
	if err != nil {
		return ""
	}

	fileName := filepath.Base(item.VirtualPath)

	for _, record := range history.Records {
		if record.DownloadID == "" {
			continue
		}

		// Match by path or filename
		// Data contains either ImportedPath or DroppedPath depending on the event
		if record.Data.ImportedPath == item.VirtualPath ||
			record.Data.DroppedPath == item.VirtualPath ||
			filepath.Base(record.Data.ImportedPath) == fileName ||
			filepath.Base(record.Data.DroppedPath) == fileName ||
			strings.Contains(record.SourceTitle, item.NzbName) {
			return record.DownloadID
		}
	}

	return ""
}

func (w *Worker) harvestSonarrDownloadID(ctx context.Context, instance *model.ConfigInstance, item *database.ImportHistory) string {
	client, err := w.clients.GetOrCreateSonarrClient(instance.Name, instance.URL, instance.APIKey)
	if err != nil {
		return ""
	}

	req := &starr.PageReq{PageSize: 50, SortKey: "date", SortDir: starr.SortDescend}
	history, err := client.GetHistoryPageContext(ctx, req)
	if err != nil {
		return ""
	}

	fileName := filepath.Base(item.VirtualPath)

	for _, record := range history.Records {
		if record.DownloadID == "" {
			continue
		}

		// Match by path or filename
		// Data contains either ImportedPath or DroppedPath depending on the event
		if record.Data.ImportedPath == item.VirtualPath ||
			record.Data.DroppedPath == item.VirtualPath ||
			filepath.Base(record.Data.ImportedPath) == fileName ||
			filepath.Base(record.Data.DroppedPath) == fileName ||
			strings.Contains(record.SourceTitle, item.NzbName) {
			return record.DownloadID
		}
	}

	return ""
}

// CleanupQueue checks all ARR instances for importPending items with empty folders
// and removes them from the queue after deleting the empty folder
func (w *Worker) CleanupQueue(ctx context.Context) error {
	cfg := w.configGetter()
	instances := w.instances.GetAllInstances()

	for _, instance := range instances {
		if !instance.Enabled {
			continue
		}

		switch instance.Type {
		case "radarr":
			if err := w.cleanupRadarrQueue(ctx, instance, cfg); err != nil {
				slog.WarnContext(ctx, "Failed to cleanup Radarr queue",
					"instance", instance.Name, "error", err)
			}
		case "sonarr":
			if err := w.cleanupSonarrQueue(ctx, instance, cfg); err != nil {
				slog.WarnContext(ctx, "Failed to cleanup Sonarr queue",
					"instance", instance.Name, "error", err)
			}
		}
	}

	return nil
}

func (w *Worker) cleanupRadarrQueue(ctx context.Context, instance *model.ConfigInstance, cfg *config.Config) error {
	client, err := w.clients.GetOrCreateRadarrClient(instance.Name, instance.URL, instance.APIKey)
	if err != nil {
		return fmt.Errorf("failed to get Radarr client: %w", err)
	}

	queue, err := client.GetQueueContext(ctx, 0, 500)
	if err != nil {
		return fmt.Errorf("failed to get Radarr queue: %w", err)
	}

	var idsToRemove []int64
	for _, q := range queue.Records {
		// Strategy 1: Ghost detection — cleanup already-imported files
		if w.checkGhostByImportHistory(ctx, q.OutputPath, cfg, instance.Name, q.Title) {
			idsToRemove = append(idsToRemove, q.ID)
			continue
		}

		// Fallback: path-gone check with safety guards
		if w.isGhostByPathGone(ctx, q.OutputPath, q.ID, cfg, instance.Name, q.Title) {
			idsToRemove = append(idsToRemove, q.ID)
			continue
		}

		// Strategy 2: Graceful cleanup for blocked/failed imports
		// Check for completed items with warning status that are pending import
		if q.Status != "completed" || q.TrackedDownloadStatus != "warning" || (q.TrackedDownloadState != "importPending" && q.TrackedDownloadState != "importBlocked") {
			continue
		}

		// Check if path is within managed directories (import_dir, mount_path, or complete_dir)
		if !w.isPathManaged(q.OutputPath, cfg) {
			continue
		}

		// Check status messages for known issues
		shouldCleanup := false
		for _, msg := range q.StatusMessages {
			allMessages := strings.Join(msg.Messages, " ")

			// Automatic import failure cleanup (configurable)
			if cfg.Arrs.CleanupAutomaticImportFailure != nil && *cfg.Arrs.CleanupAutomaticImportFailure &&
				strings.Contains(allMessages, "Automatic import is not possible") {
				shouldCleanup = true
				break
			}

			// Check configured allowlist
			for _, allowedMsg := range cfg.Arrs.QueueCleanupAllowlist {
				if allowedMsg.Enabled && (strings.Contains(allMessages, allowedMsg.Message) || strings.Contains(msg.Title, allowedMsg.Message)) {
					shouldCleanup = true
					break
				}
			}

			if shouldCleanup {
				break
			}
		}

		if shouldCleanup {
			key := fmt.Sprintf("%s|%d", instance.Name, q.ID)
			w.firstSeenMu.Lock()
			seenTime, exists := w.firstSeen[key]
			if !exists {
				w.firstSeen[key] = time.Now()
				w.firstSeenMu.Unlock()
				slog.DebugContext(ctx, "First saw failed import pending item, starting grace period",
					"path", q.OutputPath, "title", q.Title, "instance", instance.Name)
				continue
			}
			w.firstSeenMu.Unlock()

			gracePeriod := time.Duration(cfg.Arrs.QueueCleanupGracePeriodMinutes) * time.Minute
			if time.Since(seenTime) < gracePeriod {
				slog.DebugContext(ctx, "Item still in grace period",
					"path", q.OutputPath, "title", q.Title, "instance", instance.Name,
					"remaining", gracePeriod-time.Since(seenTime))
				continue
			}

			slog.InfoContext(ctx, "Found failed import pending item after grace period",
				"path", q.OutputPath, "title", q.Title, "instance", instance.Name)
			idsToRemove = append(idsToRemove, q.ID)

			w.firstSeenMu.Lock()
			delete(w.firstSeen, key)
			w.firstSeenMu.Unlock()
		} else {
			// If it's no longer matching failure criteria, remove from tracking
			key := fmt.Sprintf("%s|%d", instance.Name, q.ID)
			w.firstSeenMu.Lock()
			delete(w.firstSeen, key)
			w.firstSeenMu.Unlock()
		}
	}

	// Remove from ARR queue with removeFromClient and blocklist flags
	if len(idsToRemove) > 0 {
		removeFromClient := true
		opts := &starr.QueueDeleteOpts{
			RemoveFromClient: &removeFromClient,
			BlockList:        false,
			SkipRedownload:   false,
		}
		for _, id := range idsToRemove {
			if err := client.DeleteQueueContext(ctx, id, opts); err != nil {
				if strings.Contains(err.Error(), "404") {
					slog.DebugContext(ctx, "Queue item already removed from Radarr", "id", id)
				} else {
					slog.ErrorContext(ctx, "Failed to delete queue item",
						"id", id, "error", err)
				}
			}
		}
		slog.InfoContext(ctx, "Cleaned up Radarr queue items",
			"instance", instance.Name, "count", len(idsToRemove))
	}
	return nil
}

func (w *Worker) cleanupSonarrQueue(ctx context.Context, instance *model.ConfigInstance, cfg *config.Config) error {
	client, err := w.clients.GetOrCreateSonarrClient(instance.Name, instance.URL, instance.APIKey)
	if err != nil {
		return fmt.Errorf("failed to get Sonarr client: %w", err)
	}

	queue, err := client.GetQueueContext(ctx, 0, 500)
	if err != nil {
		return fmt.Errorf("failed to get Sonarr queue: %w", err)
	}

	var idsToRemove []int64
	for _, q := range queue.Records {
		// Strategy 1: Immediate cleanup for already imported files
		if w.checkGhostByImportHistory(ctx, q.OutputPath, cfg, instance.Name, q.Title) {
			idsToRemove = append(idsToRemove, q.ID)
			continue
		}

		// Fallback: path-gone check with safety guards
		if w.isGhostByPathGone(ctx, q.OutputPath, q.ID, cfg, instance.Name, q.Title) {
			idsToRemove = append(idsToRemove, q.ID)
			continue
		}

		// Strategy 2: Graceful cleanup for blocked/failed imports
		// Check for completed items with warning status that are pending import
		if q.Protocol != "usenet" || q.Status != "completed" || q.TrackedDownloadStatus != "warning" || (q.TrackedDownloadState != "importPending" && q.TrackedDownloadState != "importBlocked") {
			continue
		}

		// Check if path is within managed directories (import_dir, mount_path, or complete_dir)
		if !w.isPathManaged(q.OutputPath, cfg) {
			continue
		}

		// Check status messages for known issues
		shouldCleanup := false
		for _, msg := range q.StatusMessages {
			allMessages := strings.Join(msg.Messages, " ")

			// Automatic import failure cleanup (configurable)
			if cfg.Arrs.CleanupAutomaticImportFailure != nil && *cfg.Arrs.CleanupAutomaticImportFailure &&
				strings.Contains(allMessages, "Automatic import is not possible") {
				shouldCleanup = true
				break
			}

			// Check configured allowlist
			for _, allowedMsg := range cfg.Arrs.QueueCleanupAllowlist {
				if allowedMsg.Enabled && (strings.Contains(allMessages, allowedMsg.Message) || strings.Contains(msg.Title, allowedMsg.Message)) {
					shouldCleanup = true
					break
				}
			}

			if shouldCleanup {
				break
			}
		}

		if shouldCleanup {
			key := fmt.Sprintf("%s|%d", instance.Name, q.ID)
			w.firstSeenMu.Lock()
			seenTime, exists := w.firstSeen[key]
			if !exists {
				w.firstSeen[key] = time.Now()
				w.firstSeenMu.Unlock()
				slog.DebugContext(ctx, "First saw failed import pending item, starting grace period",
					"path", q.OutputPath, "title", q.Title, "instance", instance.Name)
				continue
			}
			w.firstSeenMu.Unlock()

			gracePeriod := time.Duration(cfg.Arrs.QueueCleanupGracePeriodMinutes) * time.Minute
			if time.Since(seenTime) < gracePeriod {
				slog.DebugContext(ctx, "Item still in grace period",
					"path", q.OutputPath, "title", q.Title, "instance", instance.Name,
					"remaining", gracePeriod-time.Since(seenTime))
				continue
			}

			slog.InfoContext(ctx, "Found failed import pending item after grace period",
				"path", q.OutputPath, "title", q.Title, "instance", instance.Name)
			idsToRemove = append(idsToRemove, q.ID)

			w.firstSeenMu.Lock()
			delete(w.firstSeen, key)
			w.firstSeenMu.Unlock()
		} else {
			// If it's no longer matching failure criteria, remove from tracking
			key := fmt.Sprintf("%s|%d", instance.Name, q.ID)
			w.firstSeenMu.Lock()
			delete(w.firstSeen, key)
			w.firstSeenMu.Unlock()
		}
	}

	// Remove from ARR queue with removeFromClient and blocklist flags
	if len(idsToRemove) > 0 {
		removeFromClient := true
		opts := &starr.QueueDeleteOpts{
			RemoveFromClient: &removeFromClient,
			BlockList:        false,
			SkipRedownload:   false,
		}
		for _, id := range idsToRemove {
			if err := client.DeleteQueueContext(ctx, id, opts); err != nil {
				if strings.Contains(err.Error(), "404") {
					slog.DebugContext(ctx, "Queue item already removed from Sonarr", "id", id)
				} else {
					slog.ErrorContext(ctx, "Failed to delete queue item",
						"id", id, "error", err)
				}
			}
		}
		slog.InfoContext(ctx, "Cleaned up Sonarr queue items",
			"instance", instance.Name, "count", len(idsToRemove))
	}
	return nil
}

// checkGhostByImportHistory checks if a queue item has already been imported
// by looking up AltMount's import history. Returns true if confirmed ghost
// (i.e., the file has been moved to the library).
func (w *Worker) checkGhostByImportHistory(ctx context.Context, outputPath string, cfg *config.Config, instanceName, title string) bool {
	if outputPath == "" {
		return false
	}

	mountPath := cfg.MountPath
	virtualPath := strings.TrimPrefix(filepath.ToSlash(outputPath), filepath.ToSlash(mountPath))
	virtualPath = strings.TrimPrefix(virtualPath, "/")

	if virtualPath == "" {
		return false
	}

	history, err := w.repo.GetImportHistoryByPath(ctx, virtualPath)
	if err != nil || history == nil {
		return false
	}

	if history.LibraryPath != nil && *history.LibraryPath != "" {
		slog.InfoContext(ctx, "Found ghost queue item (confirmed moved to library), cleaning up immediately",
			"path", outputPath, "library_path", *history.LibraryPath, "title", title, "instance", instanceName)
		return true
	}

	slog.DebugContext(ctx, "Item found in history but not yet moved to library, waiting for ARR final step",
		"path", outputPath, "title", title)
	return false
}

// isGhostByPathGone checks if a queue item is a ghost by verifying the source
// path no longer exists. Applies safety checks to avoid false positives from
// transient FUSE mount issues or broken symlinks.
func (w *Worker) isGhostByPathGone(ctx context.Context, outputPath string, queueID int64, cfg *config.Config, instanceName, title string) bool {
	if outputPath == "" {
		return false
	}

	// Check if path exists via Stat (follows symlinks)
	_, statErr := os.Stat(outputPath)
	if statErr == nil {
		// Path exists — not a ghost
		return false
	}
	if !os.IsNotExist(statErr) {
		// Some other error (permission, etc.) — don't assume ghost
		return false
	}

	// Broken symlink detection: if outputPath is inside ImportDir, check Lstat.
	// If Lstat succeeds but Stat fails, it's a broken symlink, not a ghost.
	if cfg.Import.ImportDir != nil && *cfg.Import.ImportDir != "" {
		importDir := filepath.Clean(*cfg.Import.ImportDir)
		if strings.HasPrefix(filepath.Clean(outputPath), importDir) {
			_, lstatErr := os.Lstat(outputPath)
			if lstatErr == nil {
				// Lstat succeeds (file entry exists) but Stat fails (target gone) → broken symlink
				slog.DebugContext(ctx, "Broken symlink detected in import dir, not treating as ghost",
					"path", outputPath, "title", title, "instance", instanceName)
				return false
			}
		}
	}

	// Minimum observation window: require the path to be missing for >=60s
	// to guard against transient FUSE hiccups.
	ghostKey := fmt.Sprintf("ghost|%s|%d", instanceName, queueID)
	w.firstSeenMu.Lock()
	seenTime, exists := w.firstSeen[ghostKey]
	if !exists {
		w.firstSeen[ghostKey] = time.Now()
		w.firstSeenMu.Unlock()
		slog.DebugContext(ctx, "First time seeing path gone, starting observation window",
			"path", outputPath, "title", title, "instance", instanceName)
		return false
	}
	w.firstSeenMu.Unlock()

	const ghostObservationWindow = 60 * time.Second
	if time.Since(seenTime) < ghostObservationWindow {
		return false
	}

	// Clean up tracking entry
	w.firstSeenMu.Lock()
	delete(w.firstSeen, ghostKey)
	w.firstSeenMu.Unlock()

	slog.WarnContext(ctx, "Found ghost queue item (source path gone after observation window), cleaning up",
		"path", outputPath, "title", title, "instance", instanceName,
		"missing_duration", time.Since(seenTime))
	return true
}

func (w *Worker) isPathManaged(path string, cfg *config.Config) bool {
	if path == "" {
		return false
	}

	cleanPath := filepath.Clean(path)

	// Check import_dir
	if cfg.Import.ImportDir != nil && *cfg.Import.ImportDir != "" {
		importDir := filepath.Clean(*cfg.Import.ImportDir)
		if strings.HasPrefix(cleanPath, importDir) {
			return true
		}
	}

	// Check mount_path
	if cfg.MountPath != "" {
		mountPath := filepath.Clean(cfg.MountPath)
		if strings.HasPrefix(cleanPath, mountPath) {
			return true
		}
	}

	// Check sabnzbd complete_dir
	if cfg.SABnzbd.Enabled != nil && *cfg.SABnzbd.Enabled && cfg.SABnzbd.CompleteDir != "" {
		completeDir := filepath.Clean(cfg.SABnzbd.CompleteDir)
		if strings.HasPrefix(cleanPath, completeDir) {
			return true
		}
	}

	return false
}
