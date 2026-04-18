package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/javi11/altmount/internal/config"
	"github.com/javi11/altmount/internal/database"
)

// WatchQueueAdder interface for adding items to the import queue from directory watcher
type WatchQueueAdder interface {
	AddToQueue(ctx context.Context, filePath string, relativePath *string, category *string, priority *database.QueuePriority, metadata *string, downloadID *string, instanceName *string) (*database.ImportQueueItem, error)
	IsFileInQueue(ctx context.Context, filePath string) (bool, error)
}

// Watcher handles monitoring a directory for new NZB files
type Watcher struct {
	queueAdder   WatchQueueAdder
	configGetter config.ConfigGetter
	log          *slog.Logger
	stopChan     chan struct{}
	wg           sync.WaitGroup
}

// NewWatcher creates a new Watcher
func NewWatcher(queueAdder WatchQueueAdder, configGetter config.ConfigGetter) *Watcher {
	return &Watcher{
		queueAdder:   queueAdder,
		configGetter: configGetter,
		log:          slog.Default().With("component", "directory-watcher"),
		stopChan:     make(chan struct{}),
	}
}

// Start begins monitoring the watch directory for new NZB files
func (w *Watcher) Start(ctx context.Context) error {
	cfg := w.configGetter()
	if cfg.Import.WatchDir == nil || *cfg.Import.WatchDir == "" {
		return nil // Watcher disabled
	}

	watchDir := *cfg.Import.WatchDir
	if _, err := os.Stat(watchDir); os.IsNotExist(err) {
		return fmt.Errorf("watch directory does not exist: %s", watchDir)
	}

	w.log.InfoContext(ctx, "Starting directory watcher", "directory", watchDir)

	w.wg.Add(1)
	go w.watchLoop(ctx, watchDir)

	return nil
}

// Stop stops the directory watcher
func (w *Watcher) Stop() {
	close(w.stopChan)
	w.wg.Wait()
}

func (w *Watcher) watchLoop(ctx context.Context, watchDir string) {
	defer w.wg.Done()

	intervalSeconds := 10
	if w.configGetter().Import.WatchIntervalSeconds != nil {
		intervalSeconds = *w.configGetter().Import.WatchIntervalSeconds
	}
	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanDirectory(ctx, watchDir)
		}
	}
}

func (w *Watcher) scanDirectory(ctx context.Context, watchDir string) {
	err := filepath.Walk(watchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Check if it's an NZB or STRM file
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".nzb" && ext != ".strm" {
			return nil
		}

		// Check if file is already being processed or in queue
		inQueue, err := w.queueAdder.IsFileInQueue(ctx, path)
		if err != nil {
			w.log.ErrorContext(ctx, "Failed to check if file is in queue", "file", path, "error", err)
			return nil
		}

		if inQueue {
			return nil
		}

		// Process the NZB file
		if err := w.processNzb(ctx, path, watchDir); err != nil {
			w.log.ErrorContext(ctx, "Failed to process watched NZB", "file", path, "error", err)
		}

		return nil
	})

	if err != nil {
		w.log.ErrorContext(ctx, "Failed to scan watch directory", "directory", watchDir, "error", err)
	}
}

func (w *Watcher) processNzb(ctx context.Context, filePath, watchRoot string) error {
	// Calculate relative path from watch root to file's directory
	relPath, err := filepath.Rel(watchRoot, filepath.Dir(filePath))
	if err != nil {
		return fmt.Errorf("failed to calculate relative path: %w", err)
	}

	// Determine category from the relative path if it's not empty
	var category *string
	if relPath != "." && relPath != "" {
		// Use the top-level directory as the category
		parts := strings.Split(filepath.ToSlash(relPath), "/")
		if len(parts) > 0 {
			cat := parts[0]
			category = &cat
		}
	}

	// Add to queue with default priority (Normal)
	priority := database.QueuePriorityNormal
	item, err := w.queueAdder.AddToQueue(ctx, filePath, &relPath, category, &priority, nil, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to add watched NZB to queue: %w", err)
	}

	categoryValue := ""
	if category != nil {
		categoryValue = *category
	}
	w.log.InfoContext(ctx, "Added watched NZB to queue",
		"file", filePath,
		"category", categoryValue,
		"queue_id", item.ID)

	return nil
}
