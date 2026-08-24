package settings

import (
	"errors"
	"net/url"
	"strings"
)

type AISettings struct {
	Enabled           bool    `json:"enabled"`
	BaseURL           string  `json:"base_url"`
	APIKey            string  `json:"api_key"`
	Model             string  `json:"model"`
	MinConfidence     float64 `json:"min_confidence"`
	TimeoutSeconds    int     `json:"timeout_seconds"`
	AllowInsecureHTTP bool    `json:"allow_insecure_http"`
}

func NewAISettings() AISettings {
	return AISettings{MinConfidence: 0.85, TimeoutSeconds: 20}
}

func (s *AISettings) Check() {
	if s.MinConfidence < 0.5 || s.MinConfidence > 1 {
		s.MinConfidence = 0.85
	}
	if s.TimeoutSeconds < 3 || s.TimeoutSeconds > 60 {
		s.TimeoutSeconds = 20
	}
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	s.Model = strings.TrimSpace(s.Model)
}

func (s AISettings) Validate() error {
	if !s.Enabled {
		return nil
	}
	if s.BaseURL == "" || s.Model == "" {
		return errors.New("AI base URL and model are required when AI is enabled")
	}
	parsed, err := url.Parse(s.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("AI base URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("AI base URL must not contain credentials, query parameters, or fragments")
	}
	if parsed.Scheme == "http" && !s.AllowInsecureHTTP {
		return errors.New("AI base URL must use HTTPS unless insecure HTTP is explicitly allowed")
	}
	return nil
}
