package opensubtitles

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/sirupsen/logrus"
)

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
