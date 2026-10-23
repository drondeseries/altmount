package api

import (
	"context"
	"net/url"

	"github.com/gofiber/fiber/v2"
)

// Saved snapshots bypass queue TTL and online title resolution; media selection uses
// the existing stream builder, including episode filtering and download-key URLs.
// Health-sourced releases have no queue snapshot left and stream from the file
// path directly, like healthy-library streams.
func (s *Server) savedLibraryStreams(ctx context.Context, kind, imdbID, baseURL, key string, season, episode int) []fiber.Map {
	out := []fiber.Map{}
	if s.queueRepo == nil {
		return out
	}
	releases, err := s.queueRepo.LibraryReleases(ctx)
	if err != nil {
		return out
	}
	for _, r := range releases {
		if !r.Saved || r.Type != kind || r.IMDbID != imdbID {
			continue
		}
		if r.Source == "health" {
			path := r.FilePath
			if path == "" {
				continue
			}
			if kind == "series" && season > 0 && episode > 0 &&
				(r.Season != season || r.Episode != episode) &&
				!(&stremioEpisodeSelector{Season: season, Episode: episode}).matches(path) {
				continue
			}
			out = append(out, fiber.Map{
				"name":  "AltMount Saved",
				"title": r.Name,
				"url": baseURL + "/api/files/stream?path=" +
					url.QueryEscape(path) + "&download_key=" + url.QueryEscape(key),
			})
			continue
		}
		var selector *stremioEpisodeSelector
		if kind == "series" {
			selector = &stremioEpisodeSelector{Season: season, Episode: episode}
		}
		streams, err := s.buildStremioStreams(ctx, &r.Snapshot, baseURL, key, r.Name, selector)
		if err != nil {
			continue
		}
		for _, stream := range streams {
			out = append(out, fiber.Map{"name": "AltMount Saved", "title": r.Name, "url": stream.URL})
		}
	}
	return out
}
