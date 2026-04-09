package services

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	netproxy "golang.org/x/net/proxy"
)

type UpstreamProxyConfig struct {
	Enabled  bool
	ProxyURL string
}

func (c UpstreamProxyConfig) CacheKey() string {
	if !c.Enabled {
		return "disabled"
	}
	return "enabled:" + c.ProxyURL
}

func LoadUpstreamProxyConfig(ctx context.Context, settings *SettingsService) (UpstreamProxyConfig, error) {
	if settings == nil {
		return UpstreamProxyConfig{}, nil
	}

	enabled, err := settings.GetBool(ctx, SettingUpstreamProxyEnabled, false)
	if err != nil {
		return UpstreamProxyConfig{}, err
	}
	proxyURL, _, err := settings.Get(ctx, SettingUpstreamProxyURL)
	if err != nil {
		return UpstreamProxyConfig{}, err
	}

	return UpstreamProxyConfig{
		Enabled:  enabled,
		ProxyURL: strings.TrimSpace(proxyURL),
	}, nil
}

func NormalizeUpstreamProxyURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	proxyURL, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse proxy url: %w", err)
	}

	scheme := strings.ToLower(strings.TrimSpace(proxyURL.Scheme))
	switch scheme {
	case "http", "https", "socks5":
	default:
		return "", fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
	}

	if proxyURL.Hostname() == "" || proxyURL.Port() == "" {
		return "", fmt.Errorf("proxy host and port are required")
	}
	if proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
		return "", fmt.Errorf("proxy url must not contain query or fragment")
	}
	if proxyURL.Path != "" && proxyURL.Path != "/" {
		return "", fmt.Errorf("proxy url must not contain path")
	}

	proxyURL.Scheme = scheme
	proxyURL.Path = ""
	proxyURL.RawPath = ""
	proxyURL.RawQuery = ""
	proxyURL.Fragment = ""
	return proxyURL.String(), nil
}

func BuildUpstreamHTTPClient(timeout time.Duration, cfg UpstreamProxyConfig) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if cfg.Enabled {
		normalizedURL, err := NormalizeUpstreamProxyURL(cfg.ProxyURL)
		if err != nil {
			return nil, err
		}
		if normalizedURL == "" {
			return nil, fmt.Errorf("proxy url is required when proxy is enabled")
		}

		proxyURL, err := url.Parse(normalizedURL)
		if err != nil {
			return nil, fmt.Errorf("parse normalized proxy url: %w", err)
		}

		switch proxyURL.Scheme {
		case "http", "https":
			transport.Proxy = http.ProxyURL(proxyURL)
		case "socks5":
			dialContext, err := newSOCKS5DialContext(proxyURL, timeout)
			if err != nil {
				return nil, err
			}
			transport.Proxy = nil
			transport.DialContext = dialContext
		default:
			return nil, fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
		}
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}, nil
}

func newSOCKS5DialContext(proxyURL *url.URL, timeout time.Duration) (func(context.Context, string, string) (net.Conn, error), error) {
	var auth *netproxy.Auth
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		auth = &netproxy.Auth{
			User:     proxyURL.User.Username(),
			Password: password,
		}
	}

	baseDialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}

	socksDialer, err := netproxy.SOCKS5("tcp", proxyURL.Host, auth, baseDialer)
	if err != nil {
		return nil, fmt.Errorf("create socks5 dialer: %w", err)
	}

	if contextDialer, ok := socksDialer.(netproxy.ContextDialer); ok {
		return contextDialer.DialContext, nil
	}

	// 兼容仅实现 Dial 的 dialer，确保后台切代理后不需要重启服务。
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		type dialResult struct {
			conn net.Conn
			err  error
		}

		resultCh := make(chan dialResult, 1)
		go func() {
			conn, err := socksDialer.Dial(network, address)
			resultCh <- dialResult{conn: conn, err: err}
		}()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-resultCh:
			return result.conn, result.err
		}
	}, nil
}
