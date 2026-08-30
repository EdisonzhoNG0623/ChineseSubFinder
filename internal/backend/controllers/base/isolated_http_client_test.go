package base

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
)

func TestNewIsolatedHTTPClientUsesSubmittedHTTPProxyWithoutMutation(t *testing.T) {
	const username = "proxy-user"
	const password = "proxy-password"
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
		if got := r.Header.Get("Proxy-Authorization"); got != expectedAuth {
			t.Errorf("proxy authorization header was not supplied")
		}
		if r.URL.Host != "target.invalid" {
			t.Errorf("proxy target host = %q", r.URL.Host)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxyServer.Close()

	parsedProxy, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(parsedProxy.Host)
	if err != nil {
		t.Fatal(err)
	}
	proxySettings := settings.ProxySettings{
		UseProxy:              true,
		UseWhichProxyProtocol: "http",
		InputProxyAddress:     host,
		InputProxyPort:        port,
		NeedPWD:               true,
		InputProxyUsername:    username,
		InputProxyPassword:    password,
	}
	original := proxySettings
	client, err := newIsolatedHTTPClient(proxySettings, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://target.invalid/check", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if !reflect.DeepEqual(proxySettings, original) {
		t.Fatalf("submitted proxy settings mutated: got %+v want %+v", proxySettings, original)
	}
}

func TestNewIsolatedHTTPClientRejectsInvalidProxyConfiguration(t *testing.T) {
	tests := []settings.ProxySettings{
		{UseProxy: true, UseWhichProxyProtocol: "ftp", InputProxyAddress: "proxy.example", InputProxyPort: "8080"},
		{UseProxy: true, UseWhichProxyProtocol: "http", InputProxyAddress: "https://proxy.example", InputProxyPort: "8080"},
		{UseProxy: true, UseWhichProxyProtocol: "http", InputProxyAddress: "proxy.example", InputProxyPort: "70000"},
	}
	for _, test := range tests {
		if _, err := newIsolatedHTTPClient(test, time.Second); err == nil {
			t.Fatalf("expected invalid proxy configuration to fail: %+v", test)
		}
	}
}

func TestProbeProxyTargetsKeepsOrderAndClassifiesConnectivity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/blocked":
			w.WriteHeader(http.StatusForbidden)
		case "/proxy-auth":
			w.WriteHeader(http.StatusProxyAuthRequired)
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()

	client, err := newIsolatedHTTPClient(settings.ProxySettings{}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result := probeProxyTargets(context.Background(), client, []proxyProbeTarget{
		{name: "reachable", url: server.URL + "/blocked"},
		{name: "auth", url: server.URL + "/proxy-auth"},
		{name: "upstream", url: server.URL + "/upstream"},
	})
	if len(result.SubSiteStatus) != 3 {
		t.Fatalf("status count = %d", len(result.SubSiteStatus))
	}
	if result.SubSiteStatus[0].Name != "reachable" || !result.SubSiteStatus[0].Valid {
		t.Fatalf("target-side 4xx should prove connectivity: %+v", result.SubSiteStatus[0])
	}
	if result.SubSiteStatus[1].Name != "auth" || result.SubSiteStatus[1].Valid {
		t.Fatalf("proxy authentication failure should be invalid: %+v", result.SubSiteStatus[1])
	}
	if result.SubSiteStatus[2].Name != "upstream" || result.SubSiteStatus[2].Valid {
		t.Fatalf("upstream failure should be invalid: %+v", result.SubSiteStatus[2])
	}
}

func TestNormalizedProbeURLExpandsSupplierTemplate(t *testing.T) {
	got, err := normalizedProbeURL("http://example.test/sub/%s.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://example.test/sub/00000000000000000000000000000000.json" {
		t.Fatalf("normalized URL = %q", got)
	}
}

type blockingRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn blockingRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestBindHTTPClientContextCancelsBackgroundRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	client := &http.Client{Transport: blockingRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-r.Context().Done()
		close(requestCanceled)
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	bound := bindHTTPClientContext(client, ctx)
	result := make(chan error, 1)
	go func() {
		// Model a dependency that discards its caller's context.
		request, requestErr := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://target.invalid", nil)
		if requestErr != nil {
			result <- requestErr
			return
		}
		_, requestErr = bound.Do(request)
		result <- requestErr
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not honor bound context cancellation")
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("server connection context was not canceled")
	}
}
