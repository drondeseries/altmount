package registrar

import (
	"encoding/json"
	"testing"

	"golift.io/starr"
	"golift.io/starr/lidarr"
	"golift.io/starr/radarr"
	"golift.io/starr/readarr"
	"golift.io/starr/sonarr"
)

// marshalTags returns the raw "tags" value from a marshaled payload, or a
// sentinel when the key is absent entirely:
//   - "[]"     -> present as an empty array (correct)
//   - "null"   -> present as null (Sonarr v5 rejects this)
//   - <absent> -> omitted via omitempty (also rejected by Sonarr v5)
func marshalTags(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", v, err)
	}

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("json.Unmarshal(%T): %v", v, err)
	}

	val, ok := generic["tags"]
	if !ok {
		return "<absent>"
	}

	return string(val)
}

// TestNoticeInputsNeverSerializeNullTags guards the regression that broke
// Sonarr v5 auto-registration: starr's input structs declare Tags without
// omitempty, so a nil slice marshals to "tags": null and Sonarr v5 answers
// 400 "The Tags field is required". Radarr/Lidarr/Readarr tolerate null, so
// only Sonarr v5 surfaced it.
func TestDownloadClientInputsNeverSerializeNullTags(t *testing.T) {
	tests := []struct {
		name  string
		build func() any
	}{
		{
			name: "sonarr",
			build: func() any {
				return &sonarr.DownloadClientInput{Name: "AltMount (SABnzbd)", Tags: emptyTags()}
			},
		},
		{
			name: "radarr",
			build: func() any {
				return &radarr.DownloadClientInput{Name: "AltMount (SABnzbd)", Tags: emptyTags()}
			},
		},
		{
			name: "lidarr",
			build: func() any {
				return &lidarr.DownloadClientInput{Name: "AltMount (SABnzbd)", Tags: emptyTags()}
			},
		},
		{
			name: "readarr",
			build: func() any {
				return &readarr.DownloadClientInput{Name: "AltMount (SABnzbd)", Tags: emptyTags()}
			},
		},
		{
			name: "sonarr update preserves existing tags",
			build: func() any {
				return &sonarr.DownloadClientInput{ID: 7, Name: "AltMount (SABnzbd)", Tags: preserveTags([]int{3, 9})}
			},
		},
		{
			name: "sonarr update with nil existing tags",
			build: func() any {
				return &sonarr.DownloadClientInput{ID: 7, Name: "AltMount (SABnzbd)", Tags: preserveTags(nil)}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch got := marshalTags(t, tt.build()); got {
			case "[]", "[3,9]":
				// ok: a real array, never null or absent
			default:
				t.Fatalf("tags serialized as %s, want a JSON array (Sonarr v5 rejects null/absent tags)", got)
			}
		})
	}
}

// TestNotificationInputCarriesUserTags verifies the webhook path keeps tags a
// user attached to an existing connection. NotificationInput declares
// `json:"tags,omitempty"`, so the empty case is simply omitted here; the
// guarantee that Sonarr v5 still receives a tags array is enforced at the wire
// layer by marshalV5Payload (see clients/v5_payload_test.go).
func TestNotificationInputCarriesUserTags(t *testing.T) {
	notif := &sonarr.NotificationInput{
		ID:   4,
		Name: "AltMount Webhook",
		Tags: preserveTags([]int{2}),
	}

	if got := marshalTags(t, notif); got != "[2]" {
		t.Fatalf("tags serialized as %s, want [2] so an existing connection keeps its tags", got)
	}
}

// TestPreserveTagsDoesNotStripUserTags pins the update-path contract: a
// re-registration must carry over tags the user attached, and only substitute
// an empty array when the ARR reported none.
func TestPreserveTagsDoesNotStripUserTags(t *testing.T) {
	if got := preserveTags(nil); got == nil || len(got) != 0 {
		t.Fatalf("preserveTags(nil) = %#v, want non-nil empty slice", got)
	}

	existing := []int{5, 11}
	got := preserveTags(existing)
	if len(got) != 2 || got[0] != 5 || got[1] != 11 {
		t.Fatalf("preserveTags(%v) = %v, want the same tags preserved", existing, got)
	}
}

// TestEmptyTagsIsNonNil ensures the add-path helper never returns nil, which
// is the exact condition that produces "tags": null.
func TestEmptyTagsIsNonNil(t *testing.T) {
	got := emptyTags()
	if got == nil {
		t.Fatal("emptyTags() returned nil; marshals to null and breaks Sonarr v5")
	}
	if len(got) != 0 {
		t.Fatalf("emptyTags() = %v, want empty", got)
	}
}

// TestFieldInputHasNoTagsAssertion documents that starr's FieldInput is a
// name/value pair, not an ARR entity, so it carries no tags field to fix.
func TestFieldInputHasNoTagsAssertion(t *testing.T) {
	raw, err := json.Marshal(&starr.FieldInput{Name: "url", Value: "http://altmount:8080"})
	if err != nil {
		t.Fatalf("json.Marshal(FieldInput): %v", err)
	}
	if string(raw) != `{"name":"url","value":"http://altmount:8080"}` {
		t.Fatalf("unexpected FieldInput payload: %s", raw)
	}
}
