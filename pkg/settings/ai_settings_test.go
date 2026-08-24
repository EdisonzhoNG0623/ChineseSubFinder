package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAISettingsValidation(t *testing.T) {
	settings := NewAISettings()
	settings.Enabled, settings.BaseURL, settings.Model = true, "http://127.0.0.1:11434/v1", "local-model"
	if err := settings.Validate(); err == nil {
		t.Fatal("plain HTTP should require explicit opt-in")
	}
	settings.AllowInsecureHTTP = true
	if err := settings.Validate(); err != nil {
		t.Fatalf("explicit local HTTP should be valid: %v", err)
	}
	settings.BaseURL = "https://user:password@example.com/v1"
	if err := settings.Validate(); err == nil {
		t.Fatal("credentials in base URL must be rejected")
	}
}

func TestMaskAndRestoreSecrets(t *testing.T) {
	current := NewSettings(t.TempDir())
	current.UserInfo.Password = "user-secret"
	current.EmbySettings.APIKey = "emby-secret"
	current.AdvancedSettings.ProxySettings.InputProxyPassword = "proxy-secret"
	current.AdvancedSettings.TmdbApiSettings.ApiKey = "tmdb-secret"
	current.SubtitleSources.AssrtSettings.Token = "assrt-secret"
	current.SubtitleSources.SubtitleBestSettings.ApiKey = "subtitle-best-secret"
	current.SubtitleSources.SubDLSettings.ApiKey = "subdl-secret"
	current.ExperimentalFunction.ApiKeySettings.Key = "api-secret"
	current.ExperimentalFunction.AISettings.APIKey = "ai-secret"
	MaskSecrets(current)
	serialized, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user-secret", "emby-secret", "proxy-secret", "tmdb-secret", "assrt-secret",
		"subtitle-best-secret", "subdl-secret", "api-secret", "ai-secret"} {
		if strings.Contains(string(serialized), secret) {
			t.Fatalf("secret %q was not masked", secret)
		}
	}
	incoming := NewSettings(t.TempDir())
	incoming.UserInfo.Password = noPassword4Show
	incoming.EmbySettings.APIKey = noPassword4Show
	incoming.AdvancedSettings.ProxySettings.InputProxyPassword = noPassword4Show
	incoming.AdvancedSettings.TmdbApiSettings.ApiKey = noPassword4Show
	incoming.SubtitleSources.AssrtSettings.Token = noPassword4Show
	incoming.SubtitleSources.SubtitleBestSettings.ApiKey = noPassword4Show
	incoming.SubtitleSources.SubDLSettings.ApiKey = noPassword4Show
	incoming.ExperimentalFunction.ApiKeySettings.Key = noPassword4Show
	incoming.ExperimentalFunction.AISettings.APIKey = noPassword4Show
	original := NewSettings(t.TempDir())
	original.UserInfo.Password = "user-secret"
	original.EmbySettings.APIKey = "emby-secret"
	original.AdvancedSettings.ProxySettings.InputProxyPassword = "proxy-secret"
	original.AdvancedSettings.TmdbApiSettings.ApiKey = "tmdb-secret"
	original.SubtitleSources.AssrtSettings.Token = "assrt-secret"
	original.SubtitleSources.SubtitleBestSettings.ApiKey = "subtitle-best-secret"
	original.SubtitleSources.SubDLSettings.ApiKey = "subdl-secret"
	original.ExperimentalFunction.ApiKeySettings.Key = "api-secret"
	original.ExperimentalFunction.AISettings.APIKey = "ai-secret"
	RestoreMaskedSecrets(incoming, original)
	if incoming.UserInfo.Password != "user-secret" || incoming.EmbySettings.APIKey != "emby-secret" ||
		incoming.AdvancedSettings.ProxySettings.InputProxyPassword != "proxy-secret" ||
		incoming.AdvancedSettings.TmdbApiSettings.ApiKey != "tmdb-secret" ||
		incoming.SubtitleSources.AssrtSettings.Token != "assrt-secret" ||
		incoming.SubtitleSources.SubtitleBestSettings.ApiKey != "subtitle-best-secret" ||
		incoming.ExperimentalFunction.ApiKeySettings.Key != "api-secret" ||
		incoming.ExperimentalFunction.AISettings.APIKey != "ai-secret" ||
		incoming.SubtitleSources.SubDLSettings.ApiKey != "subdl-secret" {
		t.Fatal("masked secrets were not restored")
	}
}

func TestCheckNormalizesMissingNestedSettings(t *testing.T) {
	s := &Settings{}
	s.Check()
	if s.CommonSettings == nil || s.AdvancedSettings == nil || s.AdvancedSettings.TaskQueue == nil ||
		s.ExperimentalFunction == nil {
		t.Fatalf("settings were not normalized: %+v", s)
	}
	if s.ExperimentalFunction.AISettings.MinConfidence != 0.85 ||
		s.ExperimentalFunction.AISettings.TimeoutSeconds != 20 {
		t.Fatalf("unexpected AI defaults: %+v", s.ExperimentalFunction.AISettings)
	}
}
