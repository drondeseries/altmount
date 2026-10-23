package clients

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golift.io/starr"
	"golift.io/starr/sonarr"
)

// strictSonarrV5Validation mirrors the real Sonarr v5 contract that produced
// the reported failure: tags must be present as an array. It returns the exact
// ASP.NET validation error body and 400 status that Sonarr itself returns.
func strictSonarrV5Validation(t *testing.T, body map[string]json.RawMessage) (int, string) {
	t.Helper()

	raw, ok := body["tags"]
	if !ok || string(raw) == "null" {
		return http.StatusBadRequest,
			`{"title":"One or more validation errors occurred.","status":400,` +
				`"errors":{"tags":["The Tags field is required."]}}`
	}

	var arr []any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return http.StatusBadRequest, `{"errors":{"tags":["The Tags field is required."]}}`
	}

	return http.StatusOK, `{"id":1,"name":"AltMount (SABnzbd)"}`
}

// TestSonarrV5DownloadClientRegistration_NoNullTags reproduces the reported
// bug end to end: AltMount registering itself as a SABnzbd download client
// against a Sonarr v5 server that validates tags the way the real one does.
//
// Before the fix this returned:
//
//	invalid status code, 400 >= 300, {"errors":{"tags":["The Tags field is required."]}}
//
// because sonarr.DownloadClientInput.Tags has no omitempty and was left nil,
// serializing as "tags": null.
func TestSonarrV5DownloadClientRegistration_NoNullTags(t *testing.T) {
	var gotBody map[string]json.RawMessage

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/downloadclient" && r.URL.Path != "/api/v5/downloadclient/test" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotBody = body

		status, payload := strictSonarrV5Validation(t, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	cfg := &starr.Config{URL: srv.URL, APIKey: "test", Client: srv.Client()}
	client := NewSonarrV5Client(cfg)

	// Exactly the payload shape registrar/manager.go builds: no Tags set at the
	// call site, so the client layer must normalize it.
	dc := &sonarr.DownloadClientInput{
		Name:                     "AltMount (SABnzbd)",
		Implementation:           "SABnzbd",
		ConfigContract:           "SABnzbdSettings",
		Enable:                   true,
		RemoveCompletedDownloads: true,
		RemoveFailedDownloads:    true,
		Priority:                 1,
		Protocol:                 "Usenet",
		Fields: []*starr.FieldInput{
			{Name: "host", Value: "altmount"},
			{Name: "port", Value: 8080},
			{Name: "apiKey", Value: "test"},
			{Name: "tvCategory", Value: "tv"},
		},
	}

	if _, err := client.AddDownloadClientContext(context.Background(), dc); err != nil {
		t.Fatalf("AddDownloadClientContext rejected by strict v5 validation: %v", err)
	}

	if got := string(gotBody["tags"]); got != "[]" {
		t.Fatalf("tags sent as %s, want []", got)
	}

	// The verification path POSTs to /downloadclient/test with the same shape.
	if err := client.TestDownloadClientContext(context.Background(), dc); err != nil {
		t.Fatalf("TestDownloadClientContext rejected by strict v5 validation: %v", err)
	}
}

// TestSonarrV5DownloadClientAddSendsForceSave pins the query parameter that
// lets registration land even when Sonarr's inline reachability probe fails,
// matching starr's own Add behaviour.
func TestSonarrV5DownloadClientAddSendsForceSave(t *testing.T) {
	var gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "AltMount (SABnzbd)"})
	}))
	defer srv.Close()

	cfg := &starr.Config{URL: srv.URL, APIKey: "test", Client: srv.Client()}
	client := NewSonarrV5Client(cfg)

	dc := &sonarr.DownloadClientInput{Name: "AltMount (SABnzbd)", Implementation: "SABnzbd"}
	if _, err := client.AddDownloadClientContext(context.Background(), dc); err != nil {
		t.Fatalf("AddDownloadClientContext: %v", err)
	}

	if gotQuery != "forceSave=true" {
		t.Fatalf("query = %q, want forceSave=true", gotQuery)
	}
}
