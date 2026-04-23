package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/javi11/altmount/internal/arrs/model"
	"golift.io/starr"
	"golift.io/starr/radarr"
	"golift.io/starr/sonarr"
)

// DiscoverFileMetadata attempts to find the rich metadata for a file by searching ARR instances
func (m *Manager) DiscoverFileMetadata(ctx context.Context, filePath, relativePath, nzbName string) (*model.WebhookMetadata, error) {
	// Strategy 1: Global History Discovery (Ultra-Precision via Release Name)
	// This is the most reliable way to find the instance and IDs, as it works even after renames.
	if nzbName != "" {
		slog.DebugContext(ctx, "Attempting primary Discovery via NZB name history", "nzb_name", nzbName)
		cleanNzbName := strings.TrimSuffix(nzbName, ".nzb")
		allInstances := m.instances.GetAllInstances()

		for _, inst := range allInstances {
			if !inst.Enabled {
				continue
			}

			switch inst.Type {
			case "radarr":
				client, err := m.clients.GetOrCreateRadarrClient(inst.Name, inst.URL, inst.APIKey)
				if err == nil {
					if meta, err := m.searchRadarrHistory(ctx, client, cleanNzbName, filePath, inst.Name); err == nil {
						slog.InfoContext(ctx, "History Discovery Success: Found file IDs via release name", "instance", inst.Name, "nzb_name", nzbName)
						return meta, nil
					}
				}
			case "sonarr":
				client, err := m.clients.GetOrCreateSonarrClient(inst.Name, inst.URL, inst.APIKey)
				if err == nil {
					if meta, err := m.searchSonarrHistory(ctx, client, cleanNzbName, filePath, inst.Name); err == nil {
						slog.InfoContext(ctx, "History Discovery Success: Found file IDs via release name", "instance", inst.Name, "nzb_name", nzbName)
						return meta, nil
					}
				}
			}
		}
	}

	// Strategy 2: Path-Based Fallback
	// If NZB name is missing or history search failed, fall back to guessing which instance owns the path.
	slog.DebugContext(ctx, "History discovery failed or NZB missing, falling back to path-based detection", "path", filePath)
	instanceType, instanceName, err := m.findInstanceForFilePath(ctx, filePath, relativePath)
	if err == nil {
		instanceConfig, err := m.instances.FindConfigInstance(instanceType, instanceName)
		if err == nil && instanceConfig.Enabled {
			switch instanceType {
			case "radarr":
				client, err := m.clients.GetOrCreateRadarrClient(instanceName, instanceConfig.URL, instanceConfig.APIKey)
				if err == nil {
					if meta, err := m.discoverRadarrMetadata(ctx, client, filePath, relativePath, nzbName, instanceName); err == nil {
						return meta, nil
					}
				}
			case "sonarr":
				client, err := m.clients.GetOrCreateSonarrClient(instanceName, instanceConfig.URL, instanceConfig.APIKey)
				if err == nil {
					if meta, err := m.discoverSonarrMetadata(ctx, client, filePath, relativePath, nzbName, instanceName); err == nil {
						return meta, nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("failed to discover metadata for file: %s (nzb: %s)", filePath, nzbName)
}

func (m *Manager) discoverRadarrMetadata(ctx context.Context, client *radarr.Radarr, filePath, relativePath, nzbName, instanceName string) (*model.WebhookMetadata, error) {
	// Strategy 1: History Search (High Precision via Release Name)
	if nzbName != "" {
		cleanNzbName := strings.TrimSuffix(nzbName, ".nzb")
		if meta, err := m.searchRadarrHistory(ctx, client, cleanNzbName, filePath, instanceName); err == nil {
			return meta, nil
		}
	}

	// Strategy 2: Library Search (Fuzzy Match via Filename/Path)
	movies, err := m.data.GetMovies(ctx, client, instanceName)
	if err != nil {
		return nil, err
	}

	fileName := filepath.Base(filePath)
	cleanFileName := strings.ReplaceAll(fileName, ".", " ")

	for _, movie := range movies {
		if !movie.HasFile || movie.MovieFile == nil {
			continue
		}

		match := false
		if movie.MovieFile.Path == filePath {
			match = true
		} else {
			movieFileName := filepath.Base(movie.MovieFile.Path)
			if movieFileName == fileName {
				match = true
			} else if strings.Contains(cleanFileName, movie.Title) {
				// Fuzzy check if title is in filename
				match = true
			}
		}

		if match {
			metadata := &model.WebhookMetadata{
				EventType:    "Discovery",
				InstanceName: instanceName,
				FilePath:     filePath,
			}
			metadata.Movie.Id = movie.ID
			metadata.Movie.TmdbId = movie.TmdbID
			metadata.Movie.FolderPath = movie.Path
			metadata.MovieFile.Id = movie.MovieFile.ID
			metadata.MovieFile.Path = movie.MovieFile.Path
			return metadata, nil
		}
	}

	return nil, fmt.Errorf("movie not found in Radarr for path: %s", filePath)
}

func (m *Manager) discoverSonarrMetadata(ctx context.Context, client *sonarr.Sonarr, filePath, relativePath, nzbName, instanceName string) (*model.WebhookMetadata, error) {
	// Strategy 1: History Search (High Precision via Release Name)
	if nzbName != "" {
		cleanNzbName := strings.TrimSuffix(nzbName, ".nzb")
		if meta, err := m.searchSonarrHistory(ctx, client, cleanNzbName, filePath, instanceName); err == nil {
			return meta, nil
		}
	}

	// Strategy 2: Library Search (Fuzzy Match via Filename/Path)
	series, err := m.data.GetSeries(ctx, client, instanceName)
	if err != nil {
		return nil, err
	}

	fileName := filepath.Base(filePath)
	cleanFileName := strings.ReplaceAll(fileName, ".", " ")

	var targetSeries *sonarr.Series
	for _, show := range series {
		// Clean up series title for fuzzy matching (handle symbols like &)
		cleanTitle := strings.ReplaceAll(show.Title, "&", "and")
		cleanTitle = strings.ReplaceAll(cleanTitle, ":", "")
		
		if strings.Contains(filePath, show.Path) {
			targetSeries = show
			break
		} else if strings.Contains(strings.ToLower(cleanFileName), strings.ToLower(show.Title)) ||
				  strings.Contains(strings.ToLower(cleanFileName), strings.ToLower(cleanTitle)) {
			targetSeries = show
			break
		}
	}

	if targetSeries == nil {
		return nil, fmt.Errorf("series not found in Sonarr for path: %s", filePath)
	}

	episodeFiles, err := m.data.GetEpisodeFiles(ctx, client, instanceName, targetSeries.ID)
	if err != nil {
		return nil, err
	}

	for _, ef := range episodeFiles {
		match := false
		if ef.Path == filePath {
			match = true
		} else if filepath.Base(ef.Path) == fileName {
			match = true
		} else if strings.Contains(fileName, filepath.Base(ef.Path)) || strings.Contains(ef.Path, fileName) {
			match = true
		}

		if match {
			metadata := &model.WebhookMetadata{
				EventType:    "Discovery",
				InstanceName: instanceName,
				FilePath:     filePath,
			}
			metadata.Series.Id = targetSeries.ID
			metadata.Series.TvdbId = targetSeries.TvdbID
			metadata.Series.Path = targetSeries.Path
			metadata.EpisodeFile.Id = ef.ID
			metadata.EpisodeFile.Path = ef.Path
			return metadata, nil
		}
	}

	return nil, fmt.Errorf("episode file not found in Sonarr for path: %s", filePath)
}

func (m *Manager) searchRadarrHistory(ctx context.Context, client *radarr.Radarr, cleanNzbName, filePath, instanceName string) (*model.WebhookMetadata, error) {
	req := &starr.PageReq{PageSize: 100, SortKey: "date", SortDir: starr.SortDescend}
	history, err := client.GetHistoryPageContext(ctx, req)
	if err != nil {
		return nil, err
	}

	for _, record := range history.Records {
		if strings.EqualFold(record.SourceTitle, cleanNzbName) && record.EventType == "movieFileImported" {
			if movieID, movieFileID := record.MovieID, record.Data.FileID; movieID > 0 && movieFileID != "" {
				fileID, _ := strconv.ParseInt(movieFileID, 10, 64)
				if fileID > 0 {
					metadata := &model.WebhookMetadata{
						EventType:    "HistoryDiscovery",
						InstanceName: instanceName,
						FilePath:     filePath,
					}
					metadata.Movie.Id = movieID
					metadata.MovieFile.Id = fileID
					return metadata, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("not found in history")
}

func (m *Manager) searchSonarrHistory(ctx context.Context, client *sonarr.Sonarr, cleanNzbName, filePath, instanceName string) (*model.WebhookMetadata, error) {
	req := &starr.PageReq{PageSize: 100, SortKey: "date", SortDir: starr.SortDescend}
	history, err := client.GetHistoryPageContext(ctx, req)
	if err != nil {
		return nil, err
	}

	for _, record := range history.Records {
		if strings.EqualFold(record.SourceTitle, cleanNzbName) && record.EventType == "downloadFolderImported" {
			if seriesID, episodeFileID := record.SeriesID, record.Data.FileID; seriesID > 0 && episodeFileID != "" {
				fileID, _ := strconv.ParseInt(episodeFileID, 10, 64)
				if fileID > 0 {
					metadata := &model.WebhookMetadata{
						EventType:    "HistoryDiscovery",
						InstanceName: instanceName,
						FilePath:     filePath,
					}
					metadata.Series.Id = seriesID
					metadata.EpisodeFile.Id = fileID
					return metadata, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("not found in history")
}
