package settings

type OpenSubtitlesSettings struct {
	Enabled                  bool   `json:"enabled"`
	APIKey                   string `json:"api_key"`
	Username                 string `json:"username"`
	Password                 string `json:"password"`
	UseHash                  bool   `json:"use_hash"`
	IncludeAITranslated      bool   `json:"include_ai_translated"`
	IncludeMachineTranslated bool   `json:"include_machine_translated"`
}
