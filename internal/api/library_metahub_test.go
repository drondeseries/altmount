package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kipsilabs/altmount/internal/database"
	"github.com/stretchr/testify/require"
)

func TestMetahubArtworkFallback(t *testing.T) {
	// Fake CDN that serves poster for one id, and everything for another.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/poster/medium/tt123/img":
			w.WriteHeader(http.StatusOK)
		case "/poster/medium/tt9999/img":
			w.WriteHeader(http.StatusNotFound)
		case "/logo/medium/tt123/img":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := metahubBaseURL
	metahubBaseURL = srv.URL
	t.Cleanup(func() { metahubBaseURL = old })

	poster, logo, background, ok := metahubArtwork(context.Background(), "tt123")
	require.True(t, ok)
	require.Contains(t, poster, "poster/medium/tt123/img")
	require.Contains(t, logo, "logo/medium/tt123/img")
	require.Empty(t, background)

	_, _, _, ok = metahubArtwork(context.Background(), "tt9999")
	require.False(t, ok)
}

func TestApplyMetahubFallbackMergesMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	old := metahubBaseURL
	metahubBaseURL = srv.URL
	t.Cleanup(func() { metahubBaseURL = old })

	base := database.LibraryTitle{
		Type: "series", IMDbID: "tt771", Name: "Stub Name",
		Metadata: []byte(`{"id":"tt771","type":"series","name":"Stub Name","releaseInfo":"2025","videos":[1]}`),
	}
	merged, ok := applyMetahubFallback(context.Background(), "tt771", "series", base)
	require.True(t, ok)
	require.Equal(t, "Stub Name", merged.Name)

	meta := string(merged.Metadata)
	require.Contains(t, meta, `"poster"`)
	require.Contains(t, meta, `"logo"`)
	require.Contains(t, meta, `"releaseInfo":"2025"`)
	require.Contains(t, meta, `"videos":[1]`)
}
