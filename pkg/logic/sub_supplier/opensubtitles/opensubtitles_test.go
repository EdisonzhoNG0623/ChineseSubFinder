package opensubtitles

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/logic/supplier_search"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/sirupsen/logrus"
)

func resetProcessQuotaWindows(t *testing.T) {
	t.Helper()
	processQuotaWindows.mu.Lock()
	processQuotaWindows.windows = make(map[string]time.Time)
	processQuotaWindows.mu.Unlock()
	t.Cleanup(func() {
		processQuotaWindows.mu.Lock()
		processQuotaWindows.windows = make(map[string]time.Time)
		processQuotaWindows.mu.Unlock()
	})
}

func TestMovieHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movie.mkv")
	data := make([]byte, 3*movieHashBlockSize)
	for index := range data {
		data[index] = byte(index % 251)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	want := uint64(len(data))
	for _, block := range [][]byte{data[:movieHashBlockSize], data[len(data)-int(movieHashBlockSize):]} {
		for index := 0; index < len(block); index += 8 {
			want += binary.LittleEndian.Uint64(block[index : index+8])
		}
	}
	got, err := movieHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != fmtHash(want) {
		t.Fatalf("movieHash() = %q, want %q", got, fmtHash(want))
	}
}

func TestLoginKeepsCredentialsOutOfURLAndIgnoresDynamicBaseURL(t *testing.T) {
	settings.SetConfigRootPath(t.TempDir())
	cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
	cfg.Enabled, cfg.APIKey, cfg.Username, cfg.Password = true, "api-secret", "user", "password-secret"
	settings.Get().SubtitleSources.OpenSubtitlesSettings = cfg

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/login" || request.URL.RawQuery != "" {
			t.Errorf("unexpected login URL: %s", request.URL.String())
		}
		if request.Header.Get("Api-Key") != "api-secret" {
			t.Error("API key was not sent in the header")
		}
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), `"password":"password-secret"`) {
			t.Error("login body did not contain the configured password")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"token":"token-value","base_url":"evil.example"}`))
	}))
	defer server.Close()

	supplier := &Supplier{log: logrus.New(), baseURL: server.URL + "/", remaining: -1}
	if err := supplier.ensureToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if supplier.baseURL != server.URL+"/" || supplier.token != "token-value" {
		t.Fatal("login response changed the fixed API endpoint or lost the token")
	}
}

func fmtHash(value uint64) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 16)
	for index := len(out) - 1; index >= 0; index-- {
		out[index] = digits[value&15]
		value >>= 4
	}
	return string(out)
}

func TestSafeDownloadURL(t *testing.T) {
	if _, err := safeDownloadURL("https://dl.opensubtitles.com/en/download/file/1"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"http://dl.opensubtitles.com/file", "https://opensubtitles.com.evil.example/file", "https://user@dl.opensubtitles.com/file"} {
		if _, err := safeDownloadURL(value); err == nil {
			t.Fatalf("unsafe URL %q was accepted", value)
		}
	}
}

func TestSearchResponseFiltersInvalidCandidates(t *testing.T) {
	if !isChinese("zh-CN") || !isChinese("zh-TW") || isChinese("en") {
		t.Fatal("unexpected language classification")
	}
	if numericIMDbID("tt0096697") != "96697" || numericIMDbID("bad") != "" {
		t.Fatal("unexpected IMDb normalization")
	}
}

func TestDownload406SetsTypedQuotaAndAutomaticallyRecovers(t *testing.T) {
	resetProcessQuotaWindows(t)
	settings.SetConfigRootPath(t.TempDir())
	cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
	cfg.Enabled, cfg.APIKey, cfg.Username, cfg.Password = true, "api-key", "user", "password"
	settings.Get().SubtitleSources.OpenSubtitlesSettings = cfg

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/download" {
			t.Errorf("unexpected request path: %s", request.URL.Path)
		}
		writer.Header().Set("Retry-After", "3600")
		writer.WriteHeader(http.StatusNotAcceptable)
		_, _ = writer.Write([]byte(`{"message":"download limit reached"}`))
	}))
	defer server.Close()

	now := time.Date(2026, time.August, 31, 1, 0, 0, 0, time.UTC)
	supplier := &Supplier{
		log: logrus.New(), baseURL: server.URL + "/", remaining: -1,
		token: "token", tokenConfig: credentialHash(cfg), tokenExpiresAt: now.Add(time.Hour),
		now: func() time.Time { return now },
	}
	_, _, err := supplier.download(context.Background(), 42)
	var providerErr *supplier_search.SupplierError
	if !errors.As(err, &providerErr) || providerErr.Kind != supplier_search.FailureQuota {
		t.Fatalf("406 error = %v, want typed quota error", err)
	}
	wantReset := now.Add(time.Hour)
	if !providerErr.RetryAt.Equal(wantReset) || !supplier.quotaResetAt.Equal(wantReset) || !supplier.OverDailyDownloadLimit() {
		t.Fatalf("quota state = remaining:%d reset:%s error:%+v", supplier.remaining, supplier.quotaResetAt, providerErr)
	}

	now = wantReset.Add(time.Second)
	if supplier.OverDailyDownloadLimit() || supplier.remaining != -1 || !supplier.quotaResetAt.IsZero() {
		t.Fatalf("quota did not automatically recover: remaining=%d reset=%s", supplier.remaining, supplier.quotaResetAt)
	}
}

func TestQuotaWindowSurvivesSupplierRebuild(t *testing.T) {
	resetProcessQuotaWindows(t)
	settings.SetConfigRootPath(t.TempDir())
	cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
	cfg.Enabled, cfg.APIKey, cfg.Username, cfg.Password = true, "shared-api-key", "shared-user", "shared-password"
	settings.Get().SubtitleSources.OpenSubtitlesSettings = cfg

	now := time.Date(2026, time.August, 31, 2, 0, 0, 0, time.UTC)
	resetAt := now.Add(3 * time.Hour)
	first := &Supplier{log: logrus.New(), remaining: -1, now: func() time.Time { return now }}
	first.markQuotaExhausted(resetAt)

	rebuilt := &Supplier{log: logrus.New(), remaining: -1, now: func() time.Time { return now }}
	if !rebuilt.OverDailyDownloadLimit() || !rebuilt.RetryAtTime().Equal(resetAt) {
		t.Fatalf("rebuilt supplier lost shared quota: remaining=%d retryAt=%s", rebuilt.remaining, rebuilt.RetryAtTime())
	}
}

func TestQuotaWindowCredentialRotationAndExpiryRecover(t *testing.T) {
	t.Run("credential rotation", func(t *testing.T) {
		resetProcessQuotaWindows(t)
		settings.SetConfigRootPath(t.TempDir())
		cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
		cfg.Enabled, cfg.APIKey, cfg.Username, cfg.Password = true, "rotation-api-key", "rotation-user", "old-password"
		settings.Get().SubtitleSources.OpenSubtitlesSettings = cfg
		now := time.Date(2026, time.August, 31, 3, 0, 0, 0, time.UTC)
		supplier := &Supplier{log: logrus.New(), remaining: -1, now: func() time.Time { return now }}
		supplier.markQuotaExhausted(now.Add(4 * time.Hour))
		if !supplier.OverDailyDownloadLimit() {
			t.Fatal("quota setup did not become active")
		}

		cfg.Password = "new-password"
		settings.Get().SubtitleSources.OpenSubtitlesSettings = cfg
		if supplier.OverDailyDownloadLimit() || !supplier.RetryAtTime().IsZero() {
			t.Fatal("credential rotation did not release the old quota window")
		}
	})

	t.Run("expiry", func(t *testing.T) {
		resetProcessQuotaWindows(t)
		settings.SetConfigRootPath(t.TempDir())
		cfg := settings.Get().SubtitleSources.OpenSubtitlesSettings
		cfg.Enabled, cfg.APIKey, cfg.Username, cfg.Password = true, "expiry-api-key", "expiry-user", "expiry-password"
		settings.Get().SubtitleSources.OpenSubtitlesSettings = cfg
		now := time.Date(2026, time.August, 31, 4, 0, 0, 0, time.UTC)
		resetAt := now.Add(time.Hour)
		first := &Supplier{log: logrus.New(), remaining: -1, now: func() time.Time { return now }}
		first.markQuotaExhausted(resetAt)
		rebuilt := &Supplier{log: logrus.New(), remaining: -1, now: func() time.Time { return now }}
		if !rebuilt.OverDailyDownloadLimit() {
			t.Fatal("shared quota was not active before expiry")
		}

		now = resetAt.Add(time.Second)
		if rebuilt.OverDailyDownloadLimit() || !rebuilt.RetryAtTime().IsZero() || rebuilt.remaining != -1 {
			t.Fatalf("expired shared quota did not recover: remaining=%d retryAt=%s", rebuilt.remaining, rebuilt.RetryAtTime())
		}
	})
}

func TestQuotaResetFallsBackToNextUTCMidnight(t *testing.T) {
	now := time.Date(2026, time.August, 31, 22, 30, 0, 0, time.UTC)
	response := &http.Response{Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}
	got := quotaResetFromResponse(response, now)
	want := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("quota reset = %s, want %s", got, want)
	}
}
