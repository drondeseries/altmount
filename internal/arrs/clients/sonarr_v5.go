package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golift.io/starr"
	"golift.io/starr/sonarr"
)

// SonarrV5Client implements SonarrClient for Sonarr v5 servers (/api/v5/*).
type SonarrV5Client struct {
	cfg *starr.Config
}

// NewSonarrV5Client returns a SonarrClient configured for the v5 API.
func NewSonarrV5Client(cfg *starr.Config) *SonarrV5Client {
	if cfg.Client == nil {
		cfg.Client = starr.Client(starr.DefaultTimeout, false)
	}
	return &SonarrV5Client{cfg: cfg}
}

func (c *SonarrV5Client) APIVersion() string {
	return "v5"
}

func (c *SonarrV5Client) GetSystemStatus() (*sonarr.SystemStatus, error) {
	return c.GetSystemStatusContext(context.Background())
}

func (c *SonarrV5Client) GetSystemStatusContext(ctx context.Context) (*sonarr.SystemStatus, error) {
	var out sonarr.SystemStatus
	req := starr.Request{URI: "/api/v5/system/status"}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *SonarrV5Client) GetRootFoldersContext(ctx context.Context) ([]*sonarr.RootFolder, error) {
	var out []*sonarr.RootFolder
	req := starr.Request{URI: "/api/v5/rootfolder"}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *SonarrV5Client) GetAllSeriesContext(ctx context.Context) ([]*sonarr.Series, error) {
	var out []*sonarr.Series
	req := starr.Request{URI: "/api/v5/series"}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *SonarrV5Client) GetSeriesEpisodeFilesContext(ctx context.Context, seriesID int64) ([]*sonarr.EpisodeFile, error) {
	var out []*sonarr.EpisodeFile
	params := make(url.Values)
	params.Set("seriesId", strconv.FormatInt(seriesID, 10))
	req := starr.Request{URI: "/api/v5/episodefile", Query: params}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *SonarrV5Client) GetSeriesEpisodesContext(ctx context.Context, getEpisode *sonarr.GetEpisode) ([]*sonarr.Episode, error) {
	var out []*sonarr.Episode
	params := make(url.Values)
	if getEpisode != nil {
		if getEpisode.SeriesID > 0 {
			params.Set("seriesId", strconv.FormatInt(getEpisode.SeriesID, 10))
		}
		if getEpisode.SeasonNumber > 0 {
			params.Set("seasonNumber", strconv.Itoa(getEpisode.SeasonNumber))
		}
		for _, id := range getEpisode.EpisodeIDs {
			params.Add("episodeIds", strconv.FormatInt(id, 10))
		}
		if getEpisode.EpisodeFileID > 0 {
			params.Set("episodeFileId", strconv.FormatInt(getEpisode.EpisodeFileID, 10))
		}
	}
	params.Add("includeSubresources", "series")
	params.Add("includeSubresources", "episodeFile")
	if getEpisode != nil && getEpisode.IncludeImages {
		params.Add("includeSubresources", "images")
	}

	req := starr.Request{URI: "/api/v5/episode", Query: params}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *SonarrV5Client) DeleteEpisodeFileContext(ctx context.Context, episodeFileID int64) error {
	req := starr.Request{URI: fmt.Sprintf("/api/v5/episodefile/%d", episodeFileID)}
	return c.cfg.DeleteAny(ctx, req)
}

func (c *SonarrV5Client) MonitorEpisodeContext(ctx context.Context, episodeIDs []int64, monitor bool) ([]*sonarr.Episode, error) {
	body := struct {
		EpisodeIDs []int64 `json:"episodeIds"`
		Monitored  bool    `json:"monitored"`
	}{
		EpisodeIDs: episodeIDs,
		Monitored:  monitor,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var out []*sonarr.Episode
	req := starr.Request{URI: "/api/v5/episode/monitor", Body: bytes.NewReader(payload)}
	if err := c.cfg.PutInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *SonarrV5Client) SendCommandContext(ctx context.Context, cmd *sonarr.CommandRequest) (*sonarr.CommandResponse, error) {
	payload, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	var out sonarr.CommandResponse
	req := starr.Request{URI: "/api/v5/command", Body: bytes.NewReader(payload)}
	if err := c.cfg.PostInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *SonarrV5Client) SendCommandWithTrigger(ctx context.Context, body any) (*sonarr.CommandResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to encode command: %w", err)
	}
	var out sonarr.CommandResponse
	req := starr.Request{URI: "/api/v5/command", Body: bytes.NewReader(payload)}
	if err := c.cfg.PostInto(ctx, req, &out); err != nil {
		return nil, fmt.Errorf("failed to send v5 command: %w", err)
	}
	return &out, nil
}

func (c *SonarrV5Client) GetCommandsContext(ctx context.Context) ([]*sonarr.CommandResponse, error) {
	var out []*sonarr.CommandResponse
	req := starr.Request{URI: "/api/v5/command"}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *SonarrV5Client) GetCommandStatusContext(ctx context.Context, commandID int64) (*sonarr.CommandResponse, error) {
	var out sonarr.CommandResponse
	req := starr.Request{URI: fmt.Sprintf("/api/v5/command/%d", commandID)}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type v5QueueResponse struct {
	Page         int              `json:"page"`
	PageSize     int              `json:"pageSize"`
	TotalRecords int              `json:"totalRecords"`
	Records      []*v5QueueRecord `json:"records"`
}

type v5QueueRecord struct {
	HasPostImportCategory   bool                   `json:"downloadClientHasPostImportCategory"`
	ID                      int64                  `json:"id"`
	SeriesID                int64                  `json:"seriesId"`
	EpisodeIDs              []int64                `json:"episodeIds"`
	SeasonNumbers           []int                  `json:"seasonNumbers"`
	Language                *starr.Value           `json:"language"`
	Quality                 *starr.Quality         `json:"quality"`
	Title                   string                 `json:"title"`
	Size                    float64                `json:"size"`
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

func (c *SonarrV5Client) GetQueueContext(ctx context.Context, records, perPage int) (*SonarrQueue, error) {
	queue := &SonarrQueue{Records: []*SonarrQueueRecord{}}
	pageSize := perPage
	if pageSize <= 0 {
		pageSize = 500
	}
	if records > 0 && records < pageSize {
		pageSize = records
	}

	seenIDs := make(map[int64]struct{})
	for page := 1; ; page++ {
		params := make(url.Values)
		params.Set("page", strconv.Itoa(page))
		params.Set("pageSize", strconv.Itoa(pageSize))
		params.Set("sortKey", "timeleft")
		params.Set("sortDirection", "ascending")
		params.Set("includeUnknownSeriesItems", "true")
		params.Add("includeSubresources", "series")
		params.Add("includeSubresources", "episodes")

		var resp v5QueueResponse
		req := starr.Request{URI: "/api/v5/queue", Query: params}
		if err := c.cfg.GetInto(ctx, req, &resp); err != nil {
			return nil, err
		}

		if len(resp.Records) == 0 {
			break
		}

		for _, r := range resp.Records {
			if r == nil {
				continue
			}
			if _, seen := seenIDs[r.ID]; seen {
				continue
			}
			seenIDs[r.ID] = struct{}{}

			rec := &SonarrQueueRecord{
				HasPostImportCategory:   r.HasPostImportCategory,
				ID:                      r.ID,
				SeriesID:                r.SeriesID,
				EpisodeIDs:              r.EpisodeIDs,
				SeasonNumbers:           r.SeasonNumbers,
				Language:                r.Language,
				Quality:                 r.Quality,
				Title:                   r.Title,
				Size:                    r.Size,
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
				IsFullSeason:            r.IsFullSeason,
				EpisodesWithFilesCount:  r.EpisodesWithFilesCount,
			}
			if len(r.EpisodeIDs) > 0 {
				rec.EpisodeID = r.EpisodeIDs[0]
			}
			queue.Records = append(queue.Records, rec)
			if records > 0 && len(queue.Records) >= records {
				break
			}
		}

		if (records > 0 && len(queue.Records) >= records) ||
			(resp.TotalRecords > 0 && len(seenIDs) >= resp.TotalRecords) ||
			len(resp.Records) < pageSize {
			queue.Page = 1
			queue.PageSize = len(queue.Records)
			queue.TotalRecords = len(queue.Records)
			break
		}
	}

	return queue, nil
}

func (c *SonarrV5Client) DeleteQueueContext(ctx context.Context, queueID int64, opts *starr.QueueDeleteOpts) error {
	req := starr.Request{
		URI:   fmt.Sprintf("/api/v5/queue/%d", queueID),
		Query: opts.Values(),
	}
	return c.cfg.DeleteAny(ctx, req)
}

func (c *SonarrV5Client) GetHistoryPageContext(ctx context.Context, params *starr.PageReq) (*sonarr.History, error) {
	q := params.Params()
	if dir := q.Get("sortDir"); dir != "" && q.Get("sortDirection") == "" {
		q.Set("sortDirection", dir)
	}
	if sID := q.Get("seriesId"); sID != "" && q.Get("seriesIds") == "" {
		q.Set("seriesIds", sID)
	}
	var out sonarr.History
	req := starr.Request{URI: "/api/v5/history", Query: q}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// postNoContent issues a POST to endpoints that return 204 No Content without attempting JSON decoding.
func (c *SonarrV5Client) postNoContent(ctx context.Context, uri string, body io.Reader) error {
	req := starr.Request{URI: translateV5URI(uri), Body: body}
	resp, err := c.cfg.Post(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *SonarrV5Client) FailContext(ctx context.Context, historyID int64) error {
	return c.postNoContent(ctx, fmt.Sprintf("/api/v5/history/failed/%d", historyID), nil)
}

func (c *SonarrV5Client) GetNotificationsContext(ctx context.Context) ([]*sonarr.NotificationOutput, error) {
	var out []*sonarr.NotificationOutput
	req := starr.Request{URI: "/api/v5/connection"}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *SonarrV5Client) AddNotificationContext(ctx context.Context, notif *sonarr.NotificationInput) (*sonarr.NotificationOutput, error) {
	payload, err := marshalV5Payload(notif)
	if err != nil {
		return nil, err
	}
	var out sonarr.NotificationOutput
	req := starr.Request{URI: "/api/v5/connection", Body: bytes.NewReader(payload)}
	if err := c.cfg.PostInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *SonarrV5Client) UpdateNotificationContext(ctx context.Context, notif *sonarr.NotificationInput) (*sonarr.NotificationOutput, error) {
	payload, err := marshalV5Payload(notif)
	if err != nil {
		return nil, err
	}
	var out sonarr.NotificationOutput
	req := starr.Request{URI: fmt.Sprintf("/api/v5/connection/%d", notif.ID), Body: bytes.NewReader(payload)}
	if err := c.cfg.PutInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *SonarrV5Client) GetDownloadClientsContext(ctx context.Context) ([]*sonarr.DownloadClientOutput, error) {
	var out []*sonarr.DownloadClientOutput
	req := starr.Request{URI: "/api/v5/downloadclient"}
	if err := c.cfg.GetInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *SonarrV5Client) AddDownloadClientContext(ctx context.Context, dc *sonarr.DownloadClientInput) (*sonarr.DownloadClientOutput, error) {
	payload, err := marshalV5Payload(dc)
	if err != nil {
		return nil, err
	}
	var out sonarr.DownloadClientOutput
	// forceSave mirrors starr's own Add (which sends ?forceSave=true): persist
	// the client even if Sonarr's immediate reachability test fails, so
	// AltMount's follow-up verification step reports the real error instead of
	// registration never landing at all.
	params := make(url.Values)
	params.Set("forceSave", "true")
	req := starr.Request{URI: "/api/v5/downloadclient", Query: params, Body: bytes.NewReader(payload)}
	if err := c.cfg.PostInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *SonarrV5Client) UpdateDownloadClientContext(ctx context.Context, dc *sonarr.DownloadClientInput, force bool) (*sonarr.DownloadClientOutput, error) {
	payload, err := marshalV5Payload(dc)
	if err != nil {
		return nil, err
	}
	params := make(url.Values)
	if force {
		params.Set("skipTesting", "true")
		params.Set("skipValidation", "all")
	}
	var out sonarr.DownloadClientOutput
	req := starr.Request{
		URI:   fmt.Sprintf("/api/v5/downloadclient/%d", dc.ID),
		Query: params,
		Body:  bytes.NewReader(payload),
	}
	if err := c.cfg.PutInto(ctx, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *SonarrV5Client) TestDownloadClientContext(ctx context.Context, dc *sonarr.DownloadClientInput) error {
	payload, err := marshalV5Payload(dc)
	if err != nil {
		return err
	}
	return c.postNoContent(ctx, "/api/v5/downloadclient/test", bytes.NewReader(payload))
}

// marshalV5Payload encodes an ARR input while guaranteeing that array-valued
// keys Sonarr v5 validates are present as JSON arrays rather than null/absent.
//
// starr's input structs disagree on these fields: DownloadClientInput declares
// `json:"tags"` and `json:"fields"` without omitempty, so a nil slice becomes
// `null`, while NotificationInput declares `json:"tags,omitempty"`, so an
// empty slice disappears from the payload entirely. Sonarr v5 requires arrays
// and answers 400 "The ... field is required" for null or absent, so normalize
// here rather than relying on every caller to pre-seed the slices.
func marshalV5Payload(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	// tags is always required by Sonarr v5, even when the struct omitted it.
	if tags, ok := payload["tags"]; !ok || string(tags) == "null" {
		payload["tags"] = json.RawMessage("[]")
	}

	// fields is only normalized when the struct models it (a null there would
	// also be rejected); payloads without a fields concept are left alone.
	if fields, ok := payload["fields"]; ok && string(fields) == "null" {
		payload["fields"] = json.RawMessage("[]")
	}

	return json.Marshal(payload)
}

// translateV5URI maps any v3-style or bare URI onto the v5 surface.
func translateV5URI(uri string) string {
	clean := strings.TrimPrefix(uri, "/")
	if strings.HasPrefix(clean, "api/v3/") {
		return "/api/v5/" + strings.TrimPrefix(clean, "api/v3/")
	}
	if strings.HasPrefix(clean, "v3/") {
		return "/api/v5/" + strings.TrimPrefix(clean, "v3/")
	}
	if strings.HasPrefix(clean, "health") {
		return "/api/v5/health"
	}
	if !strings.HasPrefix(clean, "api/v5/") && !strings.HasPrefix(clean, "v5/") {
		return "/api/v5/" + clean
	}
	return uri
}

func (c *SonarrV5Client) GetInto(ctx context.Context, req starr.Request, output interface{}) error {
	req.URI = translateV5URI(req.URI)
	return c.cfg.GetInto(ctx, req, output)
}

func (c *SonarrV5Client) PostInto(ctx context.Context, req starr.Request, output interface{}) error {
	req.URI = translateV5URI(req.URI)
	return c.cfg.PostInto(ctx, req, output)
}
