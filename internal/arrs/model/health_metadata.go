package model

import "encoding/json"

// HealthMetadata is the read model for persisted ARR webhook and Stremio
// metadata. Keep the producer-specific write models separate so reading legacy
// field names does not change the JSON written by either producer.
type HealthMetadata struct {
	HealthMediaIdentifiers
	Movie    *HealthMediaIdentifiers `json:"movie,omitempty"`
	Series   *HealthMediaIdentifiers `json:"series,omitempty"`
	Episodes []EpisodeMetadata       `json:"episodes,omitempty"`
}

// HealthMediaIdentifiers accepts current and legacy names at either the root
// or the movie/series level. Integer fields reject fractional and string IDs.
type HealthMediaIdentifiers struct {
	TMDBID       int64 `json:"tmdbId,omitempty"`
	TVDBID       int64 `json:"tvdbId,omitempty"`
	LegacyTMDBID int64 `json:"tmdb_id,omitempty"`
	LegacyTVDBID int64 `json:"tvdb_id,omitempty"`
}

func DecodeHealthMetadata(data string) (HealthMetadata, error) {
	var metadata HealthMetadata
	err := json.Unmarshal([]byte(data), &metadata)
	return metadata, err
}

func (m HealthMetadata) MatchesTMDBID(id int64) bool {
	return id > 0 && (m.TMDBID == id || m.LegacyTMDBID == id ||
		(m.Movie != nil && (m.Movie.TMDBID == id || m.Movie.LegacyTMDBID == id)))
}

func (m HealthMetadata) MatchesTVDBID(id int64) bool {
	return id > 0 && (m.TVDBID == id || m.LegacyTVDBID == id ||
		(m.Series != nil && (m.Series.TVDBID == id || m.Series.LegacyTVDBID == id)))
}

// NeedsDiscovery preserves ARR's episode discovery requirement while accepting
// valid flat identifiers from Stremio. Missing, null and nonpositive IDs cannot
// suppress discovery just because their JSON key is present.
func (m HealthMetadata) NeedsDiscovery() bool {
	if m.Movie != nil {
		return false
	}
	if m.Series != nil {
		return len(m.Episodes) == 0
	}
	return m.TMDBID <= 0 && m.LegacyTMDBID <= 0 && m.TVDBID <= 0 && m.LegacyTVDBID <= 0
}
