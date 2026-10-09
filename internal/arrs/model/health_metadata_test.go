package model

import "testing"

func TestHealthMetadataNeedsDiscovery(t *testing.T) {
	for _, tc := range []struct {
		json string
		want bool
	}{
		{`{"tmdbId":123}`, false},
		{`{"tvdbId":123}`, false},
		{`{"tmdb_id":123}`, false},
		{`{"tvdb_id":123}`, false},
		{`{"movie":{"id":1,"tmdbId":123}}`, false},
		{`{"series":{"id":1,"tvdbId":123},"episodes":[{"id":2}]}`, false},
		{`{"series":{"tvdbId":123}}`, true},
		{`{"series":{"tvdbId":123},"tvdbId":123}`, true},
		{`{"tmdbId":null}`, true},
		{`{"tvdbId":0}`, true},
		{`{"tmdbId":-123}`, true},
		{`{"tmdbId":"123"}`, true},
		{`{"tvdbId":123.9}`, true},
		{`{}`, true},
		{`null`, true},
		{`broken`, true},
	} {
		t.Run(tc.json, func(t *testing.T) {
			metadata, err := DecodeHealthMetadata(tc.json)
			got := err != nil || metadata.NeedsDiscovery()
			if got != tc.want {
				t.Fatalf("needs discovery = %v, want %v", got, tc.want)
			}
		})
	}
}
