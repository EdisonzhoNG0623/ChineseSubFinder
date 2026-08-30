package base

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ChineseSubFinder/ChineseSubFinder/pkg"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/settings"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/backend"
	"github.com/ChineseSubFinder/ChineseSubFinder/pkg/types/common"
	xproxy "golang.org/x/net/proxy"
)

const isolatedProbeUserAgent = "ChineseSubFinder connectivity check"

type proxyProbeTarget struct {
	name string
	url  string
}

type contextBoundRoundTripper struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t contextBoundRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request.Clone(t.ctx))
}

// bindHTTPClientContext makes cancellation authoritative even for libraries
// that create requests with context.Background. The client and its transport
// are copied/wrapped rather than mutated, preserving request isolation.
func bindHTTPClientContext(client *http.Client, ctx context.Context) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bound := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	bound.Transport = contextBoundRoundTripper{ctx: ctx, base: base}
	return &bound
}

// newIsolatedHTTPClient builds a client solely from the submitted settings.
// It deliberately ignores both environment proxies and the process-wide local
// proxy bridge so a connectivity check cannot disturb active downloads.
func newIsolatedHTTPClient(proxySettings settings.ProxySettings, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 15 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: timeout,
	}

	if proxySettings.UseProxy {
		proxyAddress, err := validatedProxyAddress(proxySettings.InputProxyAddress, proxySettings.InputProxyPort)
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(proxySettings.UseWhichProxyProtocol)) {
		case "http":
			proxyURL := &url.URL{Scheme: "http", Host: proxyAddress}
			if proxySettings.InputProxyUsername != "" && proxySettings.InputProxyPassword != "" {
				proxyURL.User = url.UserPassword(proxySettings.InputProxyUsername, proxySettings.InputProxyPassword)
			}
			transport.Proxy = http.ProxyURL(proxyURL)
		case "socks5":
			var auth *xproxy.Auth
			if proxySettings.InputProxyUsername != "" && proxySettings.InputProxyPassword != "" {
				auth = &xproxy.Auth{
					User:     proxySettings.InputProxyUsername,
					Password: proxySettings.InputProxyPassword,
				}
			}
			socksDialer, err := xproxy.SOCKS5("tcp", proxyAddress, auth, dialer)
			if err != nil {
				return nil, fmt.Errorf("initialize SOCKS5 proxy: %w", err)
			}
			if contextDialer, ok := socksDialer.(xproxy.ContextDialer); ok {
				transport.DialContext = contextDialer.DialContext
			} else {
				transport.DialContext = func(_ context.Context, network, address string) (net.Conn, error) {
					return socksDialer.Dial(network, address)
				}
			}
		default:
			return nil, errors.New("proxy protocol must be http or socks5")
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}, nil
}

func validatedProxyAddress(host, port string) (string, error) {
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" || strings.Contains(host, "://") || strings.ContainsAny(host, "/?#@") {
		return "", errors.New("proxy address must be a host name or IP address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New("proxy port must be between 1 and 65535")
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port), nil
}

func configuredProxyProbeTargets(current *settings.Settings) []proxyProbeTarget {
	if current == nil || current.AdvancedSettings == nil || current.AdvancedSettings.SuppliersSettings == nil {
		return nil
	}
	suppliers := current.AdvancedSettings.SuppliersSettings
	targets := make([]proxyProbeTarget, 0, 12)
	appendSupplier := func(one *settings.OneSupplierSettings) {
		if one == nil || strings.TrimSpace(one.RootUrl) == "" {
			return
		}
		targets = append(targets, proxyProbeTarget{name: one.Name, url: one.RootUrl})
	}

	appendSupplier(suppliers.Xunlei)
	appendSupplier(suppliers.Shooter)
	if suppliers.A4k != nil && suppliers.A4k.DailyDownloadLimit != 0 {
		appendSupplier(suppliers.A4k)
	}
	if !pkg.LiteMode() {
		appendSupplier(suppliers.Zimuku)
		appendSupplier(suppliers.SubHD)
	}

	if current.SubtitleSources == nil {
		return targets
	}
	sources := current.SubtitleSources
	if sources.AssrtSettings.Enabled && strings.TrimSpace(sources.AssrtSettings.Token) != "" {
		appendSupplier(suppliers.Assrt)
	}
	if sources.SubtitleBestSettings.Enabled && strings.TrimSpace(sources.SubtitleBestSettings.ApiKey) != "" {
		appendSupplier(suppliers.SubtitleBest)
	}
	if sources.SubDLSettings.Enabled && strings.TrimSpace(sources.SubDLSettings.ApiKey) != "" {
		appendSupplier(suppliers.SubDL)
	}
	openSubtitles := sources.OpenSubtitlesSettings
	if openSubtitles.Enabled && strings.TrimSpace(openSubtitles.APIKey) != "" &&
		strings.TrimSpace(openSubtitles.Username) != "" && openSubtitles.Password != "" {
		targets = append(targets, proxyProbeTarget{name: common.SubSiteOpenSubtitles, url: common.OpenSubtitlesRootURLDef})
	}
	if sources.SubSourceSettings.Enabled && strings.TrimSpace(sources.SubSourceSettings.APIKey) != "" {
		targets = append(targets, proxyProbeTarget{name: common.SubSiteSubSource, url: common.SubSourceRootURLDef})
	}
	if sources.AnimeToshoSettings.Enabled {
		appendSupplier(suppliers.AnimeTosho)
	}
	if sources.Addic7edSettings.Enabled {
		appendSupplier(suppliers.Addic7ed)
	}
	return targets
}

func probeProxyTargets(ctx context.Context, client *http.Client, targets []proxyProbeTarget) backend.ReplyCheckStatus {
	statuses := make([]backend.SiteStatus, len(targets))
	done := make(chan struct{}, len(targets))
	for index, target := range targets {
		go func(index int, target proxyProbeTarget) {
			defer func() { done <- struct{}{} }()
			statuses[index] = backend.SiteStatus{Name: target.name}
			probeURL, err := normalizedProbeURL(target.url)
			if err != nil {
				return
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
			if err != nil {
				return
			}
			req.Header.Set("User-Agent", isolatedProbeUserAgent)
			req.Header.Set("Accept", "*/*")
			started := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			// Authentication failure came from the proxy itself. A target-side
			// 4xx still proves the requested route is reachable through it.
			if resp.StatusCode == http.StatusProxyAuthRequired || resp.StatusCode >= http.StatusInternalServerError {
				return
			}
			statuses[index].Valid = true
			statuses[index].Speed = time.Since(started).Milliseconds()
		}(index, target)
	}
	for range targets {
		<-done
	}
	return backend.ReplyCheckStatus{SubSiteStatus: statuses}
}

func normalizedProbeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	// Xunlei stores a printf-style endpoint rather than a plain root URL.
	raw = strings.ReplaceAll(raw, "%s", strings.Repeat("0", 32))
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("supplier probe URL must be an HTTP(S) URL without credentials")
	}
	return parsed.String(), nil
}
