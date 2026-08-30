package settings

import (
	"strings"
	"testing"
)

func TestSearchPolicyFingerprintTracksPublicPolicyWithoutSecrets(t *testing.T) {
	current := NewSettings(t.TempDir())
	current.SearchPolicyRevision = 7
	current.SubtitleSources.OpenSubtitlesSettings = OpenSubtitlesSettings{
		Enabled: true, APIKey: "api-secret", Username: "owner", Password: "password-secret", UseHash: true,
	}
	first := SearchPolicyFingerprint(current)
	if strings.Contains(first, "secret") || strings.Contains(first, "owner") {
		t.Fatalf("fingerprint leaked credential material: %q", first)
	}

	rotated := *current
	rotatedSources := *current.SubtitleSources
	rotated.SubtitleSources = &rotatedSources
	rotated.SubtitleSources.OpenSubtitlesSettings.APIKey = "different-secret"
	if SearchPolicyFingerprint(&rotated) != first {
		t.Fatal("raw credential unexpectedly changed the public fingerprint without a revision bump")
	}
	if !searchPolicyChanged(current, &rotated) {
		t.Fatal("credential rotation was not detected")
	}

	rotated.SearchPolicyRevision++
	if SearchPolicyFingerprint(&rotated) == first {
		t.Fatal("revision bump did not change the fingerprint")
	}
}

func TestSearchPolicyChangedIgnoresUnrelatedSettings(t *testing.T) {
	current := NewSettings(t.TempDir())
	incoming := *current
	incoming.SearchPolicyRevision = 999
	incoming.UserInfo = &UserInfo{Username: "different", Password: "different-password"}
	if searchPolicyChanged(current, &incoming) {
		t.Fatal("unrelated UI credentials changed subtitle search policy")
	}

	incoming.AdvancedSettings = NewAdvancedSettings()
	incoming.AdvancedSettings.Topic = current.AdvancedSettings.Topic + 1
	if !searchPolicyChanged(current, &incoming) {
		t.Fatal("search topic change was not detected")
	}
}

func TestSearchPolicyEndpointIdentityExcludesEmbeddedCredentials(t *testing.T) {
	first := NewSettings(t.TempDir())
	first.AdvancedSettings.SuppliersSettings.Zimuku.RootUrl = "https://user:secret@example.com/subtitles?token=first#private"
	first.ExperimentalFunction.AISettings.BaseURL = "https://ai-user:ai-secret@example.ai/v1?api_key=first"
	material := publicSearchPolicyMaterial(first)
	encoded := []byte(material.Suppliers[5].RootURL + "\x00" + material.AIEndpoint)
	for _, secret := range []string{"user", "secret", "token", "api_key", "first", "private"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("public endpoint material leaked %q: %s", secret, encoded)
		}
	}

	rotated := *first
	rotatedAdvanced := *first.AdvancedSettings
	rotatedSuppliers := *first.AdvancedSettings.SuppliersSettings
	rotatedZimuku := *first.AdvancedSettings.SuppliersSettings.Zimuku
	rotated.AdvancedSettings = &rotatedAdvanced
	rotated.AdvancedSettings.SuppliersSettings = &rotatedSuppliers
	rotated.AdvancedSettings.SuppliersSettings.Zimuku = &rotatedZimuku
	rotated.AdvancedSettings.SuppliersSettings.Zimuku.RootUrl = "https://user:changed@example.com/subtitles?token=second#other"
	if SearchPolicyFingerprint(&rotated) != SearchPolicyFingerprint(first) {
		t.Fatal("credential-only endpoint rotation changed public fingerprint without a revision bump")
	}
	if !searchPolicyChanged(first, &rotated) {
		t.Fatal("raw endpoint credential rotation was not detected in memory")
	}

	rotated.AdvancedSettings.SuppliersSettings.Zimuku.RootUrl = "https://example.com/new-path"
	if SearchPolicyFingerprint(&rotated) == SearchPolicyFingerprint(first) {
		t.Fatal("routing path change did not change public fingerprint")
	}
}
