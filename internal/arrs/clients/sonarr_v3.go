package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"golift.io/starr"
	"golift.io/starr/sonarr"
)

// SonarrV3Client implements SonarrClient for Sonarr v3/v4 servers.
type SonarrV3Client struct {
	*sonarr.Sonarr
	cfg *starr.Config
}

// NewSonarrV3Client returns a SonarrClient configured for the v3 API.
func NewSonarrV3Client(cfg *starr.Config) *SonarrV3Client {
	if cfg.Client == nil {
		cfg.Client = starr.Client(starr.DefaultTimeout, false)
	}
	return &SonarrV3Client{
		Sonarr: sonarr.New(cfg),
		cfg:    cfg,
	}
}

// NewSonarrV3ClientFromSonarr wraps an existing *sonarr.Sonarr into SonarrClient.
func NewSonarrV3ClientFromSonarr(s *sonarr.Sonarr) *SonarrV3Client {
	return &SonarrV3Client{
		Sonarr: s,
	}
}

func (c *SonarrV3Client) APIVersion() string {
	return "v3"
}

func (c *SonarrV3Client) GetQueueContext(ctx context.Context, records, perPage int) (*SonarrQueue, error) {
	q, err := c.Sonarr.GetQueueContext(ctx, records, perPage)
	if err != nil {
		return nil, err
	}
	out := &SonarrQueue{
		Page:          q.Page,
		PageSize:      q.PageSize,
		SortKey:       q.SortKey,
		SortDirection: q.SortDirection,
		TotalRecords:  q.TotalRecords,
		Records:       make([]*SonarrQueueRecord, 0, len(q.Records)),
	}
	for _, r := range q.Records {
		if r == nil {
			continue
		}
		var epIDs []int64
		if r.EpisodeID > 0 {
			epIDs = []int64{r.EpisodeID}
		}
		rec := &SonarrQueueRecord{
			HasPostImportCategory:   r.HasPostImportCategory,
			ID:                      r.ID,
			SeriesID:                r.SeriesID,
			EpisodeID:               r.EpisodeID,
			EpisodeIDs:              epIDs,
			Language:                r.Language,
			Quality:                 r.Quality,
			Size:                    r.Size,
			Title:                   r.Title,
			Sizeleft:                r.Sizeleft,
			Timeleft:                r.Timeleft,
			EstimatedCompletionTime: r.EstimatedCompletionTime,
			Status:                  r.Status,
			TrackedDownloadStatus:   r.TrackedDownloadStatus,
			TrackedDownloadState:    r.TrackedDownloadState,
			StatusMessages:          r.StatusMessages,
			DownloadID:              r.DownloadID,
			Protocol:                r.Protocol,
			DownloadClient:          r.DownloadClient,
			Indexer:                 r.Indexer,
			OutputPath:              r.OutputPath,
			ErrorMessage:            r.ErrorMessage,
		}
		out.Records = append(out.Records, rec)
	}
	return out, nil
}

func (c *SonarrV3Client) SendCommandWithTrigger(ctx context.Context, body any) (*sonarr.CommandResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to encode command: %w", err)
	}
	var out sonarr.CommandResponse
	req := starr.Request{URI: "v3/command", Body: bytes.NewReader(payload)}
	if err := c.PostInto(ctx, req, &out); err != nil {
		return nil, fmt.Errorf("failed to send v3 command: %w", err)
	}
	return &out, nil
}
