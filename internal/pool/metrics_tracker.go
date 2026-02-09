package pool

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/javi11/nntppool/v2"
)

// MetricsSnapshot represents pool metrics at a point in time with calculated values
type MetricsSnapshot struct {
	BytesDownloaded             int64            `json:"bytes_downloaded"`
	BytesUploaded               int64            `json:"bytes_uploaded"`
	ArticlesDownloaded          int64            `json:"articles_downloaded"`
	ArticlesPosted              int64            `json:"articles_posted"`
	TotalErrors                 int64            `json:"total_errors"`
	ProviderErrors              map[string]int64 `json:"provider_errors"`
	DownloadSpeedBytesPerSec    float64          `json:"download_speed_bytes_per_sec"`
	MaxDownloadSpeedBytesPerSec float64          `json:"max_download_speed_bytes_per_sec"`
	UploadSpeedBytesPerSec      float64          `json:"upload_speed_bytes_per_sec"`
	Timestamp                   time.Time        `json:"timestamp"`
}

// MetricsTracker tracks pool metrics over time and calculates rates
type MetricsTracker struct {
	pool              nntppool.UsenetConnectionPool
	repo              StatsRepository
	mu                sync.RWMutex
	samples           []metricsample
	sampleInterval    time.Duration
	retentionPeriod   time.Duration
	calculationWindow time.Duration // Window for speed calculations (shorter than retention for accuracy)
	maxDownloadSpeed  float64
	// Persistent counters (loaded from DB on start)
	initialBytesDownloaded    int64
	initialArticlesDownloaded int64
	initialBytesUploaded      int64
	initialArticlesPosted     int64
	initialProviderErrors     map[string]int64
	lastSavedBytesDownloaded  int64
	persistenceThreshold      int64 // Bytes to download before forcing a save
	cancel                    context.CancelFunc
	wg                        sync.WaitGroup
	logger                    *slog.Logger
}

// metricsample represents a single metrics sample at a point in time
type metricsample struct {
	bytesDownloaded    int64
	bytesUploaded      int64
	articlesDownloaded int64
	articlesPosted     int64
	totalErrors        int64
	providerErrors     map[string]int64
	timestamp          time.Time
}

// NewMetricsTracker creates a new metrics tracker
func NewMetricsTracker(pool nntppool.UsenetConnectionPool, repo StatsRepository) *MetricsTracker {
	mt := &MetricsTracker{
		pool:                  pool,
				repo:                  repo,
				samples:               make([]metricsample, 0, 60), // Preallocate for 60 samples
				initialProviderErrors: make(map[string]int64),
				sampleInterval:        5 * time.Second,
				retentionPeriod:       60 * time.Second,
				calculationWindow:     10 * time.Second, // Use 10s window for more accurate real-time speeds
				persistenceThreshold:  1024 * 1024 * 1024, // Save every 1GB downloaded
				logger:                slog.Default().With("component", "metrics-tracker"),
			}
		

	return mt
}

// Start begins collecting metrics samples
func (mt *MetricsTracker) Start(ctx context.Context) {
	childCtx, cancel := context.WithCancel(ctx)
	mt.cancel = cancel

	// Load initial stats from DB
	if mt.repo != nil {
		stats, err := mt.repo.GetSystemStats(ctx)
		if err != nil {
			mt.logger.ErrorContext(ctx, "Failed to load system stats from database", "error", err)
		} else {
			mt.mu.Lock()
			mt.initialBytesDownloaded = stats["bytes_downloaded"]
			mt.initialArticlesDownloaded = stats["articles_downloaded"]
			mt.initialBytesUploaded = stats["bytes_uploaded"]
			mt.initialArticlesPosted = stats["articles_posted"]
			mt.maxDownloadSpeed = float64(stats["max_download_speed"])
			mt.lastSavedBytesDownloaded = mt.initialBytesDownloaded

			// Load provider errors (prefixed with provider_error:)
			for k, v := range stats {
				if strings.HasPrefix(k, "provider_error:") {
					providerID := strings.TrimPrefix(k, "provider_error:")
					mt.initialProviderErrors[providerID] = v
				}
			}
			mt.mu.Unlock()

			mt.logger.InfoContext(ctx, "Loaded persistent system stats",
				"articles", mt.initialArticlesDownloaded,
				"bytes", mt.initialBytesDownloaded,
				"provider_stats", len(mt.initialProviderErrors))
		}
	}

	// Take initial sample
	mt.takeSample()

	// Start sampling goroutine
	mt.wg.Add(1)
	go mt.samplingLoop(childCtx)

	mt.logger.InfoContext(ctx, "Metrics tracker started",
		"sample_interval", mt.sampleInterval,
		"retention_period", mt.retentionPeriod,
	)
}

// Stop stops collecting metrics samples
func (mt *MetricsTracker) Stop() {
	if mt.cancel != nil {
		mt.cancel()
		mt.wg.Wait()
		mt.logger.InfoContext(context.Background(), "Metrics tracker stopped")
	}
}

// GetSnapshot returns the current metrics with calculated speeds
func (mt *MetricsTracker) GetSnapshot() MetricsSnapshot {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Get current snapshot from pool
	poolSnapshot := mt.pool.GetMetricsSnapshot()

	// Calculate speeds
	downloadSpeed, uploadSpeed := mt.calculateSpeeds(poolSnapshot)

	// Update max speed
	if downloadSpeed > mt.maxDownloadSpeed {
		mt.maxDownloadSpeed = downloadSpeed
	}

	// Merge provider errors
	mergedProviderErrors := make(map[string]int64)
	for k, v := range mt.initialProviderErrors {
		mergedProviderErrors[k] = v
	}
	for k, v := range poolSnapshot.ProviderErrors {
		mergedProviderErrors[k] += v
	}

	return MetricsSnapshot{
		BytesDownloaded:             poolSnapshot.BytesDownloaded + mt.initialBytesDownloaded,
		BytesUploaded:               poolSnapshot.BytesUploaded + mt.initialBytesUploaded,
		ArticlesDownloaded:          poolSnapshot.ArticlesDownloaded + mt.initialArticlesDownloaded,
		ArticlesPosted:              poolSnapshot.ArticlesPosted + mt.initialArticlesPosted,
		TotalErrors:                 poolSnapshot.TotalErrors,
		ProviderErrors:              mergedProviderErrors,
		DownloadSpeedBytesPerSec:    downloadSpeed,
		MaxDownloadSpeedBytesPerSec: mt.maxDownloadSpeed,
		UploadSpeedBytesPerSec:      uploadSpeed,
		Timestamp:                   poolSnapshot.Timestamp,
	}
}

// samplingLoop periodically samples metrics
func (mt *MetricsTracker) samplingLoop(ctx context.Context) {
	defer mt.wg.Done()
	ticker := time.NewTicker(mt.sampleInterval)
	defer ticker.Stop()

	// Use a longer interval for DB updates to avoid excessive writes
	dbUpdateTicker := time.NewTicker(1 * time.Minute)
	defer dbUpdateTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final save on shutdown
			mt.saveStats(context.Background())
			return
		case <-ticker.C:
			mt.takeSample()
		case <-dbUpdateTicker.C:
			mt.saveStats(ctx)
		}
	}
}

// saveStats persists current totals to the database
func (mt *MetricsTracker) saveStats(ctx context.Context) {
	if mt.repo == nil {
		return
	}

	snapshot := mt.GetSnapshot()

	// Prepare batch update
	stats := map[string]int64{
		"bytes_downloaded":    snapshot.BytesDownloaded,
		"articles_downloaded": snapshot.ArticlesDownloaded,
		"bytes_uploaded":      snapshot.BytesUploaded,
		"articles_posted":     snapshot.ArticlesPosted,
		"max_download_speed":  int64(snapshot.MaxDownloadSpeedBytesPerSec),
	}

	// Add provider errors to batch
	for providerID, errorCount := range snapshot.ProviderErrors {
		stats["provider_error:"+providerID] = errorCount
	}

	if err := mt.repo.BatchUpdateSystemStats(ctx, stats); err != nil {
		mt.logger.ErrorContext(ctx, "Failed to persist system stats", "error", err)
	} else {
		mt.mu.Lock()
		mt.lastSavedBytesDownloaded = snapshot.BytesDownloaded
		mt.mu.Unlock()
	}
}

// Reset resets all cumulative metrics both in memory and in the database
func (mt *MetricsTracker) Reset(ctx context.Context) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Reset in-memory cumulative values
	// Since we can't easily reset the nntppool's internal counters without recreating it,
	// we adjust our "initial" values so that Displayed = Initial + Pool becomes 0.
	poolSnapshot := mt.pool.GetMetricsSnapshot()

	mt.initialBytesDownloaded = -poolSnapshot.BytesDownloaded
	mt.initialArticlesDownloaded = -poolSnapshot.ArticlesDownloaded
	mt.initialBytesUploaded = -poolSnapshot.BytesUploaded
	mt.initialArticlesPosted = -poolSnapshot.ArticlesPosted
	mt.maxDownloadSpeed = 0
	mt.initialProviderErrors = make(map[string]int64)

	// Clear samples to reset speed calculation
	mt.samples = make([]metricsample, 0, 60)

	// Persist the reset state to database
	if mt.repo != nil {
		// We need to fetch all current keys to know what to reset (especially provider errors)
		currentStats, err := mt.repo.GetSystemStats(ctx)
		if err != nil {
			mt.logger.ErrorContext(ctx, "Failed to fetch stats for reset", "error", err)
		} else {
			resetMap := make(map[string]int64)
			for k := range currentStats {
				resetMap[k] = 0
			}
			// Ensure core keys are present
			resetMap["bytes_downloaded"] = 0
			resetMap["articles_downloaded"] = 0
			resetMap["bytes_uploaded"] = 0
			resetMap["articles_posted"] = 0
			resetMap["max_download_speed"] = 0

			if err := mt.repo.BatchUpdateSystemStats(ctx, resetMap); err != nil {
				mt.logger.ErrorContext(ctx, "Failed to persist reset stats", "error", err)
			}
		}
	}

	mt.logger.InfoContext(ctx, "Pool metrics have been reset")
	return nil
}

// takeSample captures a metrics snapshot and stores it
func (mt *MetricsTracker) takeSample() {
	snapshot := mt.pool.GetMetricsSnapshot()

	mt.mu.Lock()
	defer mt.mu.Unlock()

	// Create sample (pool-relative values are fine for speed calculation)
	sample := metricsample{
		bytesDownloaded:    snapshot.BytesDownloaded,
		bytesUploaded:      snapshot.BytesUploaded,
		articlesDownloaded: snapshot.ArticlesDownloaded,
		articlesPosted:     snapshot.ArticlesPosted,
		totalErrors:        snapshot.TotalErrors,
		providerErrors:     copyProviderErrors(snapshot.ProviderErrors),
		timestamp:          snapshot.Timestamp,
	}

	// Add sample
	mt.samples = append(mt.samples, sample)

	// Adaptive Persistence: Check if we should force a save due to high activity
	totalBytesDownloaded := snapshot.BytesDownloaded + mt.initialBytesDownloaded
	if totalBytesDownloaded-mt.lastSavedBytesDownloaded >= mt.persistenceThreshold {
		// Use a non-blocking save or a shorter context? 
		// For now, simple call is fine as it's a goroutine
		go mt.saveStats(context.Background())
	}

	// Clean up old samples
	mt.cleanupOldSamples()
}

// cleanupOldSamples removes samples older than the retention period
func (mt *MetricsTracker) cleanupOldSamples() {
	cutoff := time.Now().Add(-mt.retentionPeriod)

	// Find first sample to keep
	keepIndex := 0
	for i, sample := range mt.samples {
		if sample.timestamp.After(cutoff) {
			keepIndex = i
			break
		}
	}

	// Remove old samples
	if keepIndex > 0 {
		mt.samples = mt.samples[keepIndex:]
	}
}

// calculateSpeeds calculates download and upload speeds based on historical samples
// Uses calculationWindow (default 10s) for more accurate real-time speed measurements
func (mt *MetricsTracker) calculateSpeeds(current nntppool.PoolMetricsSnapshot) (downloadSpeed, uploadSpeed float64) {
	// Need at least 2 samples to calculate speed
	if len(mt.samples) < 2 {
		return 0, 0
	}

	// Find sample closest to calculationWindow ago (instead of using oldest sample)
	// This provides more accurate real-time speed by looking at recent history
	targetTime := current.Timestamp.Add(-mt.calculationWindow)
	compareIndex := 0

	// Search backwards to find the sample closest to calculationWindow ago
	for i := len(mt.samples) - 1; i >= 0; i-- {
		if mt.samples[i].timestamp.Before(targetTime) || mt.samples[i].timestamp.Equal(targetTime) {
			compareIndex = i
			break
		}
	}

	compareSample := mt.samples[compareIndex]

	// Calculate time delta
	timeDelta := current.Timestamp.Sub(compareSample.timestamp).Seconds()
	if timeDelta <= 0 {
		return 0, 0
	}

	// Calculate download speed (bytes per second) over the calculation window
	bytesDelta := current.BytesDownloaded - compareSample.bytesDownloaded
	if bytesDelta > 0 {
		downloadSpeed = float64(bytesDelta) / timeDelta
	}

	// Calculate upload speed (bytes per second) over the calculation window
	uploadDelta := current.BytesUploaded - compareSample.bytesUploaded
	if uploadDelta > 0 {
		uploadSpeed = float64(uploadDelta) / timeDelta
	}

	return downloadSpeed, uploadSpeed
}

// copyProviderErrors creates a copy of the provider errors map
func copyProviderErrors(original map[string]int64) map[string]int64 {
	if original == nil {
		return nil
	}

	copy := make(map[string]int64, len(original))
	for k, v := range original {
		copy[k] = v
	}
	return copy
}