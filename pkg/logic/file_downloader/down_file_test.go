package file_downloader_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	csfpkg "github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/sirupsen/logrus"
)

func TestDownFileWithTimeoutDoesNotRestartSlowDownload(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Disposition", `attachment; filename="slow.ass"`)
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(150 * time.Millisecond)
		_, _ = writer.Write([]byte("subtitle"))
	}))
	defer server.Close()

	_, _, err := csfpkg.DownFileWithTimeout(logrus.New(), server.URL, 40*time.Millisecond)
	if err == nil {
		t.Fatal("DownFileWithTimeout() error = nil, want timeout")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want exactly one non-restarted download", got)
	}
}
