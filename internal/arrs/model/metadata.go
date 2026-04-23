package model

type WebhookMetadata struct {
	EventType    string `json:"eventType"`
	InstanceName string `json:"instanceName,omitempty"`
	FilePath     string `json:"filePath,omitempty"`
	Movie        struct {
		Id         int64  `json:"id"`
		TmdbId     int64  `json:"tmdbId"`
		FolderPath string `json:"folderPath"`
	} `json:"movie"`
	MovieFile struct {
		Id        int64  `json:"id"`
		SceneName string `json:"sceneName"`
		Path      string `json:"path"`
	} `json:"movieFile"`
	Series struct {
		Id     int64  `json:"id"`
		TvdbId int64  `json:"tvdbId"`
		Path   string `json:"path"`
	} `json:"series"`
	EpisodeFile struct {
		Id        int64  `json:"id"`
		SceneName string `json:"sceneName"`
		Path      string `json:"path"`
	} `json:"episodeFile"`
}
