package settings

import (
	"errors"
	"strings"
)

type SubtitleSources struct {
	AssrtSettings         AssrtSettings         `json:"assrt_settings"`
	SubtitleBestSettings  SubtitleBestSettings  `json:"subtitle_best_settings"`
	SubDLSettings         SubDLSettings         `json:"subdl_settings"`
	OpenSubtitlesSettings OpenSubtitlesSettings `json:"open_subtitles_settings"`
	SubSourceSettings     SubSourceSettings     `json:"subsource_settings"`
	AnimeToshoSettings    PublicSourceSettings  `json:"animetosho_settings"`
	Addic7edSettings      PublicSourceSettings  `json:"addic7ed_settings"`
}

func NewSubtitleSources() *SubtitleSources {
	return &SubtitleSources{
		OpenSubtitlesSettings: OpenSubtitlesSettings{UseHash: true},
	}
}

func (s SubtitleSources) Validate() error {
	openSubtitles := s.OpenSubtitlesSettings
	if openSubtitles.Enabled && (strings.TrimSpace(openSubtitles.APIKey) == "" ||
		strings.TrimSpace(openSubtitles.Username) == "" || openSubtitles.Password == "") {
		return errors.New("OpenSubtitles requires API key, username, and password")
	}
	if s.SubSourceSettings.Enabled && strings.TrimSpace(s.SubSourceSettings.APIKey) == "" {
		return errors.New("SubSource requires an API key")
	}
	return nil
}
