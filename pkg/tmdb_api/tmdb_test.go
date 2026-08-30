package tmdb_api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestInjectedAlternateBaseURLIsRequestLocal(t *testing.T) {
	response := func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"page":1,"results":[],"total_pages":0,"total_results":0}`)),
			Request: request,
		}, nil
	}
	var alternateHost, defaultHost string
	alternateClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		alternateHost = request.URL.Host
		return response(request)
	})}
	defaultClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		defaultHost = request.URL.Host
		return response(request)
	})}
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	alternate, err := NewTmdbHelperWithHTTPClient(logger, "alternate-key", true, alternateClient)
	if err != nil || !alternate.Alive() {
		t.Fatalf("alternate Alive() = false, %v", err)
	}
	regular, err := NewTmdbHelperWithHTTPClient(logger, "default-key", false, defaultClient)
	if err != nil || !regular.Alive() {
		t.Fatalf("default Alive() = false, %v", err)
	}
	if alternateHost != alternateTMDBAPIHost {
		t.Fatalf("alternate host = %q", alternateHost)
	}
	if defaultHost != defaultTMDBAPIHost {
		t.Fatalf("default helper was polluted by alternate helper: host=%q", defaultHost)
	}
}

func TestInjectedClientDoesNotSleepOnRateLimit(t *testing.T) {
	var requests int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{"1"}},
			Body:       io.NopCloser(strings.NewReader(`{"status_code":25,"status_message":"rate limited"}`)),
			Request:    request,
		}, nil
	})}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	helper, err := NewTmdbHelperWithHTTPClient(logger, "rate-limit-key", false, client)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() { done <- helper.Alive() }()
	select {
	case alive := <-done:
		if alive {
			t.Fatal("rate-limited response reported alive")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("injected connectivity client slept in TMDB auto-retry")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestTMDBErrorsRedactAPIKey(t *testing.T) {
	const apiKey = "secret-api-key+/="
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failed request to %s", request.URL.String())
	})}
	var logs bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&logs)
	helper, err := NewTmdbHelperWithHTTPClient(logger, apiKey, false, client)
	if err != nil {
		t.Fatal(err)
	}
	if helper.Alive() {
		t.Fatal("failing transport reported alive")
	}
	if strings.Contains(logs.String(), apiKey) || strings.Contains(logs.String(), "secret-api-key") {
		t.Fatalf("TMDB API key leaked to logs: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "[REDACTED]") {
		t.Fatalf("redacted marker missing from log: %s", logs.String())
	}
}

func TestNewTmdbHelperWithHTTPClientUsesInjectedTransport(t *testing.T) {
	var requests int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		atomic.AddInt32(&requests, 1)
		if !strings.Contains(request.URL.Path, "/search/multi") {
			t.Errorf("unexpected TMDB path %q", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"page":1,"results":[],"total_pages":0,"total_results":0}`)),
			Request: request,
		}, nil
	})}
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	helper, err := NewTmdbHelperWithHTTPClient(logger, "test-api-key", false, client)
	if err != nil {
		t.Fatal(err)
	}
	if !helper.Alive() {
		t.Fatal("TMDB helper did not use the injected successful transport")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("request count = %d", got)
	}
}
