package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golift.io/starr"
	"golift.io/starr/sonarr"
)

// SonarrQueue is the normalized queue response for Sonarr v3 and v5.
type SonarrQueue struct {
	Page          int                  `json:"page"`
	PageSize      int                  `json:"pageSize"`
	SortKey       string               `json:"sortKey"`
	SortDirection string               `json:"sortDirection"`
	TotalRecords  int                  `json:"totalRecords"`
	Records       []*SonarrQueueRecord `json:"records"`
}

// SonarrQueueRecord is a single queue item normalized across Sonarr v3 and v5.
// In v3, Sonarr reports a singular EpisodeID.
// In v5, Sonarr reports EpisodeIDs ([]int64) and singular EpisodeID is absent.
// Both fields are populated here for cross-version compatibility.
type SonarrQueueRecord struct {
	HasPostImportCategory   bool                   `json:"downloadClientHasPostImportCategory"`
	ID                      int64                  `json:"id"`
	SeriesID                int64                  `json:"seriesId"`
	EpisodeID               int64                  `json:"episodeId"`
	EpisodeIDs              []int64                `json:"episodeIds"`
	SeasonNumbers           []int                  `json:"seasonNumbers"`
	Language                *starr.Value           `json:"language"`
	Quality                 *starr.Quality         `json:"quality"`
	Size                    float64                `json:"size"`
	Title                   string                 `json:"title"`
	Sizeleft                float64                `json:"sizeleft"`
	Timeleft                string                 `json:"timeleft"`
	EstimatedCompletionTime time.Time              `json:"estimatedCompletionTime"`
	Status                  string                 `json:"status"`
	TrackedDownloadStatus   string                 `json:"trackedDownloadStatus"`
	TrackedDownloadState    string                 `json:"trackedDownloadState"`
	StatusMessages          []*starr.StatusMessage `json:"statusMessages"`
	DownloadID              string                 `json:"downloadId"`
	Protocol                starr.Protocol         `json:"protocol"`
	DownloadClient          string                 `json:"downloadClient"`
	Indexer                 string                 `json:"indexer"`
	OutputPath              string                 `json:"outputPath"`
	ErrorMessage            string                 `json:"errorMessage"`
	IsFullSeason            bool                   `json:"isFullSeason"`
	EpisodesWithFilesCount  int                    `json:"episodesWithFilesCount"`
}

// SonarrClient provides a unified contract for interacting with Sonarr v3 (v3/v4 servers)
// and Sonarr v5 (v5 servers).
type SonarrClient interface {
	// APIVersion returns "v3" or "v5".
	APIVersion() string

	// System & Status
	GetSystemStatus() (*sonarr.SystemStatus, error)
	GetSystemStatusContext(ctx context.Context) (*sonarr.SystemStatus, error)

	// Root Folders
	GetRootFoldersContext(ctx context.Context) ([]*sonarr.RootFolder, error)

	// Series & Episodes
	GetAllSeriesContext(ctx context.Context) ([]*sonarr.Series, error)
	GetSeriesEpisodeFilesContext(ctx context.Context, seriesID int64) ([]*sonarr.EpisodeFile, error)
	GetSeriesEpisodesContext(ctx context.Context, getEpisode *sonarr.GetEpisode) ([]*sonarr.Episode, error)
	DeleteEpisodeFileContext(ctx context.Context, episodeFileID int64) error
	MonitorEpisodeContext(ctx context.Context, episodeIDs []int64, monitor bool) ([]*sonarr.Episode, error)

	// Commands
	SendCommandContext(ctx context.Context, cmd *sonarr.CommandRequest) (*sonarr.CommandResponse, error)
	SendCommandWithTrigger(ctx context.Context, body any) (*sonarr.CommandResponse, error)
	GetCommandsContext(ctx context.Context) ([]*sonarr.CommandResponse, error)
	GetCommandStatusContext(ctx context.Context, commandID int64) (*sonarr.CommandResponse, error)

	// Queue
	GetQueueContext(ctx context.Context, records, perPage int) (*SonarrQueue, error)
	DeleteQueueContext(ctx context.Context, queueID int64, opts *starr.QueueDeleteOpts) error

	// History
	GetHistoryPageContext(ctx context.Context, params *starr.PageReq) (*sonarr.History, error)
	FailContext(ctx context.Context, historyID int64) error

	// Notifications / Connections
	GetNotificationsContext(ctx context.Context) ([]*sonarr.NotificationOutput, error)
	AddNotificationContext(ctx context.Context, notif *sonarr.NotificationInput) (*sonarr.NotificationOutput, error)
	UpdateNotificationContext(ctx context.Context, notif *sonarr.NotificationInput) (*sonarr.NotificationOutput, error)

	// Download Clients
	GetDownloadClientsContext(ctx context.Context) ([]*sonarr.DownloadClientOutput, error)
	AddDownloadClientContext(ctx context.Context, dc *sonarr.DownloadClientInput) (*sonarr.DownloadClientOutput, error)
	UpdateDownloadClientContext(ctx context.Context, dc *sonarr.DownloadClientInput, force bool) (*sonarr.DownloadClientOutput, error)
	TestDownloadClientContext(ctx context.Context, dc *sonarr.DownloadClientInput) error

	// Raw starr request passthrough (used by health checks)
	GetInto(ctx context.Context, req starr.Request, output interface{}) error
	PostInto(ctx context.Context, req starr.Request, output interface{}) error
}

// ProbeSonarrVersion determines whether the given Sonarr instance uses the "v3" or "v5" API.
func ProbeSonarrVersion(ctx context.Context, rawURL, apiKey string, httpClient *http.Client) (string, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	baseURL := strings.TrimRight(rawURL, "/")

	var lastNetErr error

	// Helper for probing an endpoint with small body reader
	probeGet := func(path string) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("X-Api-Key", apiKey)
		req.Header.Set("Accept", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return resp.StatusCode, body, nil
	}

	// 1. Try GET /api (returns ApiInfoResource: {current: "v5", ...})
	if status, body, err := probeGet("/api"); err == nil {
		switch status {
		case http.StatusOK:
			var info struct {
				Current string `json:"current"`
			}
			if json.Unmarshal(body, &info) == nil && info.Current != "" {
				curr := strings.ToLower(info.Current)
				if strings.HasPrefix(curr, "v5") || strings.HasPrefix(curr, "5") {
					return "v5", nil
				}
				if strings.HasPrefix(curr, "v3") || strings.HasPrefix(curr, "3") || strings.HasPrefix(curr, "4") {
					return "v3", nil
				}
			}
		case http.StatusUnauthorized, http.StatusForbidden:
			return "", fmt.Errorf("authentication failed (HTTP %d): invalid API key", status)
		}
	} else {
		lastNetErr = err
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// 2. Try GET /api/v5/system/status
	if status, body, err := probeGet("/api/v5/system/status"); err == nil {
		switch status {
		case http.StatusOK:
			var sys struct {
				AppName string `json:"appName"`
				Version string `json:"version"`
			}
			if json.Unmarshal(body, &sys) == nil {
				if strings.EqualFold(sys.AppName, "sonarr") || sys.Version != "" {
					return "v5", nil
				}
			}
		case http.StatusUnauthorized, http.StatusForbidden:
			return "", fmt.Errorf("authentication failed (HTTP %d): invalid API key", status)
		}
	} else {
		lastNetErr = err
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// 3. Try GET /api/v3/system/status
	if status, body, err := probeGet("/api/v3/system/status"); err == nil {
		switch status {
		case http.StatusOK:
			var sys struct {
				AppName string `json:"appName"`
				Version string `json:"version"`
			}
			if json.Unmarshal(body, &sys) == nil {
				if strings.EqualFold(sys.AppName, "sonarr") || sys.Version != "" {
					return "v3", nil
				}
			}
		case http.StatusUnauthorized, http.StatusForbidden:
			return "", fmt.Errorf("authentication failed (HTTP %d): invalid API key", status)
		}
	} else {
		lastNetErr = err
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	if lastNetErr != nil {
		return "", fmt.Errorf("failed to reach Sonarr at %s: %w", baseURL, lastNetErr)
	}

	return "", fmt.Errorf("failed to detect Sonarr API version at %s: neither /api, /api/v5, nor /api/v3 returned valid Sonarr status", baseURL)
}

// NewSonarrClient creates a SonarrClient for the specified version ("v3" or "v5").
// If version is empty or "auto", ProbeSonarrVersion is used.
func NewSonarrClient(ctx context.Context, cfg *starr.Config, version string) (SonarrClient, error) {
	if cfg.Client == nil {
		cfg.Client = starr.Client(starr.DefaultTimeout, false)
	}

	v := strings.ToLower(strings.TrimSpace(version))
	switch v {
	case "v5", "5":
		return NewSonarrV5Client(cfg), nil
	case "v3", "3", "v4", "4":
		return NewSonarrV3Client(cfg), nil
	case "", "auto":
		probed, err := ProbeSonarrVersion(ctx, cfg.URL, cfg.APIKey, cfg.Client)
		if err != nil {
			return nil, err
		}
		if probed == "v5" {
			return NewSonarrV5Client(cfg), nil
		}
		return NewSonarrV3Client(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported sonarr api version: %q (expected 'auto', 'v3', or 'v5')", version)
	}
}
