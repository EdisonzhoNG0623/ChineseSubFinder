package settings

type SubtitleSources struct {
	AssrtSettings        AssrtSettings        `json:"assrt_settings"`
	SubtitleBestSettings SubtitleBestSettings `json:"subtitle_best_settings"`
	SubDLSettings        SubDLSettings        `json:"subdl_settings"`
}

func NewSubtitleSources() *SubtitleSources {
	return &SubtitleSources{}
}
