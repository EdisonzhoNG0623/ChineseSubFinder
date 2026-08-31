package notify_center

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/log_helper"
)

func TestNewNotifyCenter(t *testing.T) {

	var requests int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		atomic.AddInt64(&requests, 1)
	}))
	defer server.Close()
	center := NewNotifyCenter(log_helper.GetLogger4Tester(), server.URL+"/")
	center.Add("groupName", "Info asd 哈哈")
	center.Send()
	if atomic.LoadInt64(&requests) != 1 {
		t.Fatalf("webhook requests = %d, want 1", atomic.LoadInt64(&requests))
	}
}
