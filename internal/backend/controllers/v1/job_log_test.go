package v1

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	"github.com/gin-gonic/gin"
)

func TestJobLogFilePathAllowsOnlySafeIDs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Logs")
	for _, jobID := range []string{
		strings.Repeat("a", 64),
		"batch-1723456789",
		"legacy_job_01",
	} {
		t.Run("valid_"+jobID, func(t *testing.T) {
			got, ok := jobLogFilePath(root, jobID)
			if !ok {
				t.Fatalf("jobLogFilePath(%q) rejected a valid job ID", jobID)
			}
			want := filepath.Join(root, common.OnceLogPrefix+jobID+".log")
			if got != want {
				t.Fatalf("jobLogFilePath(%q) = %q, want %q", jobID, got, want)
			}
		})
	}

	invalidIDs := []string{
		"",
		"../secret",
		"/../../../var/log/auth",
		"nested/job",
		`nested\job`,
		"job.log",
		"job\x00id",
		"任务",
		strings.Repeat("a", maxJobLogIDLength+1),
	}
	for _, jobID := range invalidIDs {
		if got, ok := jobLogFilePath(root, jobID); ok {
			t.Errorf("jobLogFilePath(%q) = %q, want rejection", jobID, got)
		}
	}
}

func TestJobLogHandlerRejectsPathTraversal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	controller := &ControllerBase{}
	router.POST("/job-log", controller.JobLogHandler)

	request := httptest.NewRequest(http.MethodPost, "/job-log", bytes.NewBufferString(`{"id":"/../../../var/log/auth"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "invalid job id") {
		t.Fatalf("body = %q, want invalid job id", response.Body.String())
	}
}
