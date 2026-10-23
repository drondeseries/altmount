package clients

import (
	"encoding/json"
	"testing"

	"golift.io/starr/sonarr"
)

// tagsValue extracts the raw "tags" value from a marshaled payload.
// Returns "<absent>" when the key is missing entirely.
func tagsValue(t *testing.T, raw []byte) string {
	t.Helper()

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("json.Unmarshal: %v (payload: %s)", err, raw)
	}

	val, ok := generic["tags"]
	if !ok {
		return "<absent>"
	}

	return string(val)
}

// TestMarshalV5PayloadAlwaysEmitsTagsArray is the regression guard for the
// Sonarr v5 auto-registration failure ("The Tags field is required").
//
// starr's input structs disagree on the tags json tag:
//   - DownloadClientInput: `json:"tags"`  (no omitempty) -> nil becomes "tags": null
//   - NotificationInput:   `json:"tags,omitempty"`       -> empty is dropped entirely
//
// Sonarr v5 rejects both. marshalV5Payload must normalize either shape into a
// present JSON array.
func TestMarshalV5PayloadAlwaysEmitsTagsArray(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		wantTags string
	}{
		{
			name:     "download client with nil tags",
			input:    &sonarr.DownloadClientInput{Name: "AltMount (SABnzbd)", Tags: nil},
			wantTags: "[]",
		},
		{
			name:     "download client with empty tags",
			input:    &sonarr.DownloadClientInput{Name: "AltMount (SABnzbd)", Tags: []int{}},
			wantTags: "[]",
		},
		{
			name:     "download client preserves real tag ids",
			input:    &sonarr.DownloadClientInput{ID: 3, Name: "AltMount (SABnzbd)", Tags: []int{4, 8}},
			wantTags: "[4,8]",
		},
		{
			name:     "notification with nil tags (omitempty would drop the key)",
			input:    &sonarr.NotificationInput{Name: "AltMount Webhook", Tags: nil},
			wantTags: "[]",
		},
		{
			name:     "notification with empty tags",
			input:    &sonarr.NotificationInput{Name: "AltMount Webhook", Tags: []int{}},
			wantTags: "[]",
		},
		{
			name:     "notification preserves real tag ids",
			input:    &sonarr.NotificationInput{ID: 2, Name: "AltMount Webhook", Tags: []int{6}},
			wantTags: "[6]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := marshalV5Payload(tt.input)
			if err != nil {
				t.Fatalf("marshalV5Payload: %v", err)
			}

			got := tagsValue(t, raw)
			if got == "null" {
				t.Fatalf("tags serialized as null; Sonarr v5 rejects this (payload: %s)", raw)
			}
			if got == "<absent>" {
				t.Fatalf("tags key absent; Sonarr v5 rejects this (payload: %s)", raw)
			}
			if got != tt.wantTags {
				t.Fatalf("tags = %s, want %s (payload: %s)", got, tt.wantTags, raw)
			}
		})
	}
}

// TestMarshalV5PayloadPreservesOtherFields ensures the normalization pass does
// not silently drop or mangle the rest of the payload (it round-trips through
// map[string]json.RawMessage).
func TestMarshalV5PayloadPreservesOtherFields(t *testing.T) {
	in := &sonarr.DownloadClientInput{
		Name:           "AltMount (SABnzbd)",
		Implementation: "SABnzbd",
		ConfigContract: "SABnzbdSettings",
		Protocol:       "Usenet",
		Priority:       1,
		Enable:         true,
	}

	raw, err := marshalV5Payload(in)
	if err != nil {
		t.Fatalf("marshalV5Payload: %v", err)
	}

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	for _, key := range []string{"name", "implementation", "configContract", "protocol", "priority", "enable", "fields", "tags"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("field %q missing from payload: %s", key, raw)
		}
	}

	// A live Sonarr v5 would reject null for fields[] too, so pin it here.
	if got := string(generic["fields"]); got == "null" {
		t.Errorf("fields serialized as null, which Sonarr v5 also rejects: %s", raw)
	}
}
