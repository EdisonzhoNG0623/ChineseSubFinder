package notify_center

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
)

func TestNewNotifyCenter(t *testing.T) {

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	center := NewNotifyCenter(log_helper.GetLogger4Tester(), server.URL+"/")
	center.Add("groupName", "Info asd 哈哈")
	center.Send()
	if requests.Load() != 1 {
		t.Fatalf("webhook requests = %d, want 1", requests.Load())
	}
}
