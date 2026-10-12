package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golift.io/starr"
	"golift.io/starr/sonarr"
)

// v5MockServer simulates a Sonarr v5 instance that ONLY serves /api/v5/* routes
// and rejects any /api/v3/* routes with 404.
type v5MockServer struct {
	mu            sync.Mutex
	paths         []string
	headers       map[string][]string
	postedBody    map[string]any
	deletedParams map[string]string
}

func newV5MockServer() *v5MockServer {
	return &v5MockServer{
		headers:       make(map[string][]string),
		deletedParams: make(map[string]string),
	}
}

func (m *v5MockServer) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()

		m.paths = append(m.paths, r.Method+" "+r.URL.Path)
		m.headers[r.URL.Path] = append(m.headers[r.URL.Path], r.Header.Get("X-Api-Key"))

		// Reject any v3 routes to ensure v5 client never falls back to v3 routes
		if strings.Contains(r.URL.Path, "/v3/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "v3 not supported on this v5 server"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"current": "5.0.0",
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/system/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"appName":      "Sonarr",
				"version":      "5.0.0.9999",
				"instanceName": "SonarrV5Test",
				"branch":       "develop",
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/rootfolder":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "path": "/data/tv", "accessible": true, "freeSpace": 1000000000},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/series":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 10, "title": "Test Series V5", "path": "/data/tv/Test Series V5", "tvdbId": 12345},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/episodefile":
			seriesID, _ := strconv.ParseInt(r.URL.Query().Get("seriesId"), 10, 64)
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 20, "seriesId": seriesID, "path": "/data/tv/Test Series V5/S01E01.mkv", "relativePath": "S01E01.mkv"},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/episode":
			// Verify v5 includeSubresources
			sub := r.URL.Query()["includeSubresources"]
			var hasSeries, hasEpisodeFile bool
			for _, s := range sub {
				if s == "series" {
					hasSeries = true
				}
				if s == "episodeFile" {
					hasEpisodeFile = true
				}
			}
			if !hasSeries || !hasEpisodeFile {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "missing includeSubresources"}`))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 30, "seriesId": 10, "seasonNumber": 1, "episodeNumber": 1, "title": "Pilot", "episodeFileId": 20},
			})

		case r.Method == http.MethodDelete && r.URL.Path == "/api/v5/episodefile/20":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPut && r.URL.Path == "/api/v5/episode/monitor":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.postedBody = body
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 30, "monitored": body["monitored"]},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/v5/command":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.postedBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      100,
				"name":    body["name"],
				"status":  "queued",
				"trigger": body["trigger"],
				"body":    body,
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/command":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 100, "name": "EpisodeSearch", "status": "completed"},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/command/100":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 100, "name": "EpisodeSearch", "status": "completed",
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/queue":
			// Assert v5 params
			if r.URL.Query().Get("sortDirection") != "ascending" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "sortDirection must be ascending"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page":         1,
				"pageSize":     500,
				"totalRecords": 2,
				"records": []map[string]any{
					{
						"id":                     501,
						"seriesId":               10,
						"episodeIds":             []int{30, 31},
						"seasonNumbers":          []int{1},
						"title":                  "Multi.Episode.Release",
						"trackedDownloadStatus":  "warning",
						"trackedDownloadState":   "importBlocked",
						"downloadClient":         "AltMount",
						"downloadId":             "DL-1",
						"status":                 "completed",
						"isFullSeason":           false,
						"episodesWithFilesCount": 0,
					},
					{
						"id":                     502,
						"seriesId":               10,
						"episodeIds":             []int{30, 31, 32, 33},
						"seasonNumbers":          []int{1},
						"title":                  "Full.Season.Pack",
						"trackedDownloadStatus":  "ok",
						"trackedDownloadState":   "downloading",
						"downloadClient":         "AltMount",
						"downloadId":             "DL-2",
						"status":                 "downloading",
						"isFullSeason":           true,
						"episodesWithFilesCount": 2,
					},
				},
			})

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v5/queue/"):
			m.deletedParams[r.URL.Path] = r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page":         1,
				"pageSize":     10,
				"totalRecords": 1,
				"records": []map[string]any{
					{
						"id":          700,
						"seriesId":    10,
						"episodeId":   30,
						"sourceTitle": "Test.Release",
						"eventType":   "grabbed",
						"downloadId":  "DL-1",
						"data": map[string]string{
							"indexer": "NZBGeek",
						},
					},
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/v5/history/failed/700":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/connection":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 80, "name": "AltMount Webhook", "implementation": "Webhook", "onGrab": true, "onDownload": true},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/v5/connection":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.postedBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   81,
				"name": body["name"],
			})

		case r.Method == http.MethodPut && r.URL.Path == "/api/v5/connection/80":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.postedBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   80,
				"name": body["name"],
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/downloadclient":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 90, "name": "AltMount", "implementation": "SABnzbd", "enable": true},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/v5/downloadclient":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.postedBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   91,
				"name": body["name"],
			})

		case r.Method == http.MethodPut && r.URL.Path == "/api/v5/downloadclient/90":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			m.postedBody = body
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   90,
				"name": body["name"],
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/v5/downloadclient/test":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v5/health":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"source": "Test", "type": "ok", "message": "All good"},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "route not found"}`))
		}
	}))
}

func TestSonarrV5Client_EndToEnd(t *testing.T) {
	mock := newV5MockServer()
	srv := mock.server()
	defer srv.Close()

	ctx := context.Background()
	cfg := &starr.Config{URL: srv.URL, APIKey: "test-v5-key", Client: srv.Client()}

	client, err := NewSonarrClient(ctx, cfg, "v5")
	if err != nil {
		t.Fatalf("failed to create Sonarr v5 client: %v", err)
	}

	if client.APIVersion() != "v5" {
		t.Errorf("expected APIVersion v5, got %s", client.APIVersion())
	}

	// 1. Status
	status, err := client.GetSystemStatusContext(ctx)
	if err != nil {
		t.Fatalf("GetSystemStatusContext failed: %v", err)
	}
	if status.AppName != "Sonarr" || status.Version != "5.0.0.9999" {
		t.Errorf("unexpected status: %+v", status)
	}

	// 2. Root Folders
	roots, err := client.GetRootFoldersContext(ctx)
	if err != nil {
		t.Fatalf("GetRootFoldersContext failed: %v", err)
	}
	if len(roots) != 1 || roots[0].Path != "/data/tv" {
		t.Errorf("unexpected roots: %+v", roots)
	}

	// 3. Series & Episodes
	series, err := client.GetAllSeriesContext(ctx)
	if err != nil {
		t.Fatalf("GetAllSeriesContext failed: %v", err)
	}
	if len(series) != 1 || series[0].Title != "Test Series V5" {
		t.Errorf("unexpected series: %+v", series)
	}

	files, err := client.GetSeriesEpisodeFilesContext(ctx, 10)
	if err != nil {
		t.Fatalf("GetSeriesEpisodeFilesContext failed: %v", err)
	}
	if len(files) != 1 || files[0].RelativePath != "S01E01.mkv" {
		t.Errorf("unexpected files: %+v", files)
	}

	eps, err := client.GetSeriesEpisodesContext(ctx, &sonarr.GetEpisode{SeriesID: 10, SeasonNumber: 1})
	if err != nil {
		t.Fatalf("GetSeriesEpisodesContext failed: %v", err)
	}
	if len(eps) != 1 || eps[0].Title != "Pilot" {
		t.Errorf("unexpected episodes: %+v", eps)
	}

	if err := client.DeleteEpisodeFileContext(ctx, 20); err != nil {
		t.Fatalf("DeleteEpisodeFileContext failed: %v", err)
	}

	monEps, err := client.MonitorEpisodeContext(ctx, []int64{30}, false)
	if err != nil {
		t.Fatalf("MonitorEpisodeContext failed: %v", err)
	}
	if len(monEps) != 1 || monEps[0].Monitored {
		t.Errorf("unexpected monitor response: %+v", monEps)
	}

	// 4. Commands
	cmdResp, err := client.SendCommandContext(ctx, &sonarr.CommandRequest{Name: "RefreshMonitoredDownloads"})
	if err != nil {
		t.Fatalf("SendCommandContext failed: %v", err)
	}
	if cmdResp.ID != 100 {
		t.Errorf("expected command ID 100, got %d", cmdResp.ID)
	}

	trigResp, err := client.SendCommandWithTrigger(ctx, map[string]any{"name": "EpisodeSearch", "trigger": "manual"})
	if err != nil {
		t.Fatalf("SendCommandWithTrigger failed: %v", err)
	}
	if trigResp.Trigger != "manual" {
		t.Errorf("expected trigger manual, got %s", trigResp.Trigger)
	}

	cmds, err := client.GetCommandsContext(ctx)
	if err != nil || len(cmds) != 1 {
		t.Fatalf("GetCommandsContext failed: %v", err)
	}

	cmdStatus, err := client.GetCommandStatusContext(ctx, 100)
	if err != nil || cmdStatus.Status != "completed" {
		t.Fatalf("GetCommandStatusContext failed: %v", err)
	}

	// 5. Queue: multi-episode and season pack normalization
	queue, err := client.GetQueueContext(ctx, 0, 500)
	if err != nil {
		t.Fatalf("GetQueueContext failed: %v", err)
	}
	if len(queue.Records) != 2 {
		t.Fatalf("expected 2 queue records, got %d", len(queue.Records))
	}
	r1 := queue.Records[0]
	if r1.ID != 501 || r1.EpisodeID != 30 || len(r1.EpisodeIDs) != 2 || r1.EpisodeIDs[1] != 31 {
		t.Errorf("record 1 normalization mismatch: ID=%d, EpID=%d, EpIDs=%v", r1.ID, r1.EpisodeID, r1.EpisodeIDs)
	}
	r2 := queue.Records[1]
	if !r2.IsFullSeason || len(r2.EpisodeIDs) != 4 || r2.EpisodesWithFilesCount != 2 {
		t.Errorf("record 2 season pack mismatch: FullSeason=%v, EpIDs=%v, withFiles=%d",
			r2.IsFullSeason, r2.EpisodeIDs, r2.EpisodesWithFilesCount)
	}

	// 6. Delete Queue with options
	delOpts := &starr.QueueDeleteOpts{
		BlockList:      true,
		SkipRedownload: true,
	}
	if err := client.DeleteQueueContext(ctx, 501, delOpts); err != nil {
		t.Fatalf("DeleteQueueContext failed: %v", err)
	}
	delQuery := mock.deletedParams["/api/v5/queue/501"]
	if !strings.Contains(delQuery, "blocklist=true") || !strings.Contains(delQuery, "skipRedownload=true") {
		t.Errorf("delete query options mismatch: %s", delQuery)
	}

	// 7. History & Fail
	hist, err := client.GetHistoryPageContext(ctx, &starr.PageReq{PageSize: 10, SortDir: starr.SortDescend})
	if err != nil {
		t.Fatalf("GetHistoryPageContext failed: %v", err)
	}
	if len(hist.Records) != 1 || hist.Records[0].Data.Indexer != "NZBGeek" {
		t.Errorf("unexpected history: %+v", hist)
	}
	if err := client.FailContext(ctx, 700); err != nil {
		t.Fatalf("FailContext failed: %v", err)
	}

	// 8. Notifications / Connections
	notifs, err := client.GetNotificationsContext(ctx)
	if err != nil || len(notifs) != 1 {
		t.Fatalf("GetNotificationsContext failed: %v", err)
	}
	newNotif, err := client.AddNotificationContext(ctx, &sonarr.NotificationInput{Name: "New Webhook"})
	if err != nil || newNotif.ID != 81 {
		t.Fatalf("AddNotificationContext failed: %v", err)
	}
	updNotif, err := client.UpdateNotificationContext(ctx, &sonarr.NotificationInput{ID: 80, Name: "Updated Webhook"})
	if err != nil || updNotif.ID != 80 {
		t.Fatalf("UpdateNotificationContext failed: %v", err)
	}

	// 9. Download Clients
	dcs, err := client.GetDownloadClientsContext(ctx)
	if err != nil || len(dcs) != 1 {
		t.Fatalf("GetDownloadClientsContext failed: %v", err)
	}
	newDC, err := client.AddDownloadClientContext(ctx, &sonarr.DownloadClientInput{Name: "New DC"})
	if err != nil || newDC.ID != 91 {
		t.Fatalf("AddDownloadClientContext failed: %v", err)
	}
	updDC, err := client.UpdateDownloadClientContext(ctx, &sonarr.DownloadClientInput{ID: 90, Name: "Updated DC"}, true)
	if err != nil || updDC.ID != 90 {
		t.Fatalf("UpdateDownloadClientContext failed: %v", err)
	}
	if err := client.TestDownloadClientContext(ctx, &sonarr.DownloadClientInput{Name: "Test DC"}); err != nil {
		t.Fatalf("TestDownloadClientContext failed: %v", err)
	}

	// 10. Health passthrough
	var health []any
	if err := client.GetInto(ctx, starr.Request{URI: "/health"}, &health); err != nil {
		t.Fatalf("GetInto /health failed: %v", err)
	}
	if len(health) != 1 {
		t.Errorf("expected 1 health item, got %d", len(health))
	}
}

func TestProbeSonarrVersion(t *testing.T) {
	// 1. Server responding with /api {current: "5.0.0"}
	s5 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			_ = json.NewEncoder(w).Encode(map[string]any{"current": "5.0.0"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s5.Close()

	v, err := ProbeSonarrVersion(context.Background(), s5.URL, "key", s5.Client())
	if err != nil || v != "v5" {
		t.Errorf("expected v5 from /api, got %q (err: %v)", v, err)
	}

	// 2. Server where /api is 404, but /api/v5/system/status is 200
	s5Status := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v5/system/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"appName": "Sonarr", "version": "5.0.0"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s5Status.Close()

	v, err = ProbeSonarrVersion(context.Background(), s5Status.URL, "key", s5Status.Client())
	if err != nil || v != "v5" {
		t.Errorf("expected v5 from status, got %q (err: %v)", v, err)
	}

	// 3. Server where /api/v3/system/status is 200
	s3Status := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/system/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"appName": "Sonarr", "version": "4.0.0"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s3Status.Close()

	v, err = ProbeSonarrVersion(context.Background(), s3Status.URL, "key", s3Status.Client())
	if err != nil || v != "v3" {
		t.Errorf("expected v3 from v3 status, got %q (err: %v)", v, err)
	}

	// 4. Server with invalid key (401)
	sAuth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer sAuth.Close()

	_, err = ProbeSonarrVersion(context.Background(), sAuth.URL, "bad-key", sAuth.Client())
	if err == nil {
		t.Error("expected auth error for HTTP 401")
	}

	// 5. Unreachable server (network error)
	_, err = ProbeSonarrVersion(context.Background(), "http://127.0.0.1:59999", "key", &http.Client{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Error("expected network error for unreachable server")
	}
}

func TestSonarrV5Client_PaginationMultiPage(t *testing.T) {
	// Simulate 750 total records
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))

		if pageSize != 500 {
			t.Errorf("pageSize changed during pagination: got %d, want 500", pageSize)
		}

		w.Header().Set("Content-Type", "application/json")
		var records []map[string]any
		startID := (page - 1) * pageSize
		count := 500
		if startID+count > 750 {
			count = 750 - startID
		}
		for i := 0; i < count; i++ {
			records = append(records, map[string]any{
				"id":         startID + i + 1,
				"episodeIds": []int{startID + i + 1},
				"title":      fmt.Sprintf("Item %d", startID+i+1),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"page":         page,
			"pageSize":     pageSize,
			"totalRecords": 750,
			"records":      records,
		})
	}))
	defer srv.Close()

	cfg := &starr.Config{URL: srv.URL, APIKey: "test", Client: srv.Client()}
	c := NewSonarrV5Client(cfg)

	q, err := c.GetQueueContext(context.Background(), 0, 500)
	if err != nil {
		t.Fatalf("GetQueueContext failed: %v", err)
	}
	if len(q.Records) != 750 {
		t.Fatalf("expected 750 records across pages, got %d", len(q.Records))
	}
	// Verify no duplicates and full sequence
	seen := make(map[int64]bool)
	for _, r := range q.Records {
		if seen[r.ID] {
			t.Errorf("duplicate record ID %d encountered", r.ID)
		}
		seen[r.ID] = true
	}
}

func TestNewSonarrClient_NilClientAndExplicitVersions(t *testing.T) {
	ctx := context.Background()

	// 1. Nil http client handled safely
	cfg := &starr.Config{URL: "http://localhost:8989", APIKey: "key"}
	c, err := NewSonarrClient(ctx, cfg, "v5")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if c.APIVersion() != "v5" {
		t.Errorf("expected v5, got %s", c.APIVersion())
	}
	if cfg.Client == nil {
		t.Error("expected non-nil http client to be initialized")
	}

	// 2. Explicit v3
	c3, err := NewSonarrClient(ctx, cfg, "v3")
	if err != nil || c3.APIVersion() != "v3" {
		t.Errorf("expected v3 client, got %v (err: %v)", c3, err)
	}

	// 3. Unsupported version
	_, err = NewSonarrClient(ctx, cfg, "v99")
	if err == nil {
		t.Error("expected error for unsupported version v99")
	}
}

func TestSonarrV3Client_QueueNormalization(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"page":         1,
				"pageSize":     10,
				"totalRecords": 1,
				"records": []map[string]any{
					{
						"id":                    42,
						"episodeId":             999,
						"seriesId":              12,
						"title":                 "Single.Episode.V3",
						"trackedDownloadStatus": "warning",
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s.Close()

	cfg := &starr.Config{URL: s.URL, APIKey: "test", Client: s.Client()}
	c := NewSonarrV3Client(cfg)

	if c.APIVersion() != "v3" {
		t.Errorf("expected APIVersion v3, got %s", c.APIVersion())
	}

	q, err := c.GetQueueContext(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("GetQueueContext failed: %v", err)
	}
	if len(q.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(q.Records))
	}
	rec := q.Records[0]
	if rec.EpisodeID != 999 {
		t.Errorf("expected EpisodeID 999, got %d", rec.EpisodeID)
	}
	if len(rec.EpisodeIDs) != 1 || rec.EpisodeIDs[0] != 999 {
		t.Errorf("expected EpisodeIDs [999], got %v", rec.EpisodeIDs)
	}
}
