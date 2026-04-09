package services

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"tavily-proxy/server/internal/db"
)

func newUpstreamProxyTestDeps(t *testing.T) (*SettingsService, *KeyService) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewSettingsService(database), NewKeyService(database, logger)
}

func TestTavilyProxy_HotReloadsHTTPProxySetting(t *testing.T) {
	t.Parallel()

	settings, keys := newUpstreamProxyTestDeps(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	const tavilyKey = "tvly-hot-reload-http"
	if _, err := keys.Create(ctx, tavilyKey, "primary", 1000); err != nil {
		t.Fatalf("create key: %v", err)
	}

	var upstreamCalls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamCalls, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer "+tavilyKey {
			t.Fatalf("unexpected Authorization header: got %q want %q", got, "Bearer "+tavilyKey)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"request_id":"http-proxy","results":[]}`))
	}))
	t.Cleanup(upstream.Close)

	var httpProxyCalls int32
	httpProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&httpProxyCalls, 1)

		req, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			t.Fatalf("proxy create request: %v", err)
		}
		req.Header = r.Header.Clone()
		req.RequestURI = ""

		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatalf("proxy round trip: %v", err)
		}
		defer resp.Body.Close()

		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(httpProxy.Close)

	proxy := NewTavilyProxy(upstream.URL, 5*time.Second, keys, nil, nil, logger).WithSettings(settings)

	if _, err := proxy.Do(ctx, ProxyRequest{
		Method:   http.MethodPost,
		Path:     "/search",
		Headers:  http.Header{"Content-Type": []string{"application/json"}},
		Body:     []byte(`{"query":"direct"}`),
		ClientIP: "127.0.0.1",
	}); err != nil {
		t.Fatalf("direct proxy request: %v", err)
	}
	if got := atomic.LoadInt32(&httpProxyCalls); got != 0 {
		t.Fatalf("http proxy should not be used before enabling, got %d calls", got)
	}

	if err := settings.Set(ctx, SettingUpstreamProxyURL, httpProxy.URL); err != nil {
		t.Fatalf("save proxy url: %v", err)
	}
	if err := settings.SetBool(ctx, SettingUpstreamProxyEnabled, true); err != nil {
		t.Fatalf("save proxy enabled: %v", err)
	}

	if _, err := proxy.Do(ctx, ProxyRequest{
		Method:   http.MethodPost,
		Path:     "/search",
		Headers:  http.Header{"Content-Type": []string{"application/json"}},
		Body:     []byte(`{"query":"via http proxy"}`),
		ClientIP: "127.0.0.1",
	}); err != nil {
		t.Fatalf("http proxy request: %v", err)
	}
	if got := atomic.LoadInt32(&httpProxyCalls); got == 0 {
		t.Fatalf("expected request to pass through http proxy after enabling")
	}
	if got := atomic.LoadInt32(&upstreamCalls); got != 2 {
		t.Fatalf("unexpected upstream call count: got %d want %d", got, 2)
	}
}

func TestTavilyProxy_UsesConfiguredSOCKS5Proxy(t *testing.T) {
	t.Parallel()

	settings, keys := newUpstreamProxyTestDeps(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	const tavilyKey = "tvly-socks5-proxy"
	if _, err := keys.Create(ctx, tavilyKey, "primary", 1000); err != nil {
		t.Fatalf("create key: %v", err)
	}

	var upstreamCalls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstreamCalls, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer "+tavilyKey {
			t.Fatalf("unexpected Authorization header: got %q want %q", got, "Bearer "+tavilyKey)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"request_id":"socks5-proxy","results":[]}`))
	}))
	t.Cleanup(upstream.Close)

	socksProxyAddr, socksProxyCalls := startSOCKS5Proxy(t)
	if err := settings.Set(ctx, SettingUpstreamProxyURL, "socks5://"+socksProxyAddr); err != nil {
		t.Fatalf("save proxy url: %v", err)
	}
	if err := settings.SetBool(ctx, SettingUpstreamProxyEnabled, true); err != nil {
		t.Fatalf("save proxy enabled: %v", err)
	}

	proxy := NewTavilyProxy(upstream.URL, 5*time.Second, keys, nil, nil, logger).WithSettings(settings)
	if _, err := proxy.Do(ctx, ProxyRequest{
		Method:   http.MethodPost,
		Path:     "/search",
		Headers:  http.Header{"Content-Type": []string{"application/json"}},
		Body:     []byte(`{"query":"via socks5 proxy"}`),
		ClientIP: "127.0.0.1",
	}); err != nil {
		t.Fatalf("socks5 proxy request: %v", err)
	}

	if got := atomic.LoadInt32(socksProxyCalls); got == 0 {
		t.Fatalf("expected request to pass through socks5 proxy")
	}
	if got := atomic.LoadInt32(&upstreamCalls); got != 1 {
		t.Fatalf("unexpected upstream call count: got %d want %d", got, 1)
	}
}

func startSOCKS5Proxy(t *testing.T) (string, *int32) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks5 proxy: %v", err)
	}

	var callCount int32
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		_ = ln.Close()
	})

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}
			go handleSOCKS5Conn(t, conn, &callCount)
		}
	}()

	return ln.Addr().String(), &callCount
}

func handleSOCKS5Conn(t *testing.T, clientConn net.Conn, callCount *int32) {
	t.Helper()
	defer clientConn.Close()

	header := make([]byte, 2)
	if _, err := io.ReadFull(clientConn, header); err != nil {
		return
	}
	if header[0] != 0x05 {
		t.Fatalf("unexpected socks version: %d", header[0])
	}

	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(clientConn, methods); err != nil {
		t.Fatalf("read socks methods: %v", err)
	}
	if _, err := clientConn.Write([]byte{0x05, 0x00}); err != nil {
		t.Fatalf("write socks method response: %v", err)
	}

	requestHeader := make([]byte, 4)
	if _, err := io.ReadFull(clientConn, requestHeader); err != nil {
		t.Fatalf("read socks request header: %v", err)
	}
	if requestHeader[1] != 0x01 {
		t.Fatalf("unsupported socks command: %d", requestHeader[1])
	}

	targetAddr, err := readSOCKS5Address(clientConn, requestHeader[3])
	if err != nil {
		t.Fatalf("read socks target address: %v", err)
	}

	targetConn, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		_, _ = clientConn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		t.Fatalf("dial target from socks proxy: %v", err)
	}
	defer targetConn.Close()

	atomic.AddInt32(callCount, 1)
	if _, err := clientConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("write socks success response: %v", err)
	}

	copyDone := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(targetConn, clientConn)
		if tcpConn, ok := targetConn.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		}
		copyDone <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, targetConn)
		if tcpConn, ok := clientConn.(*net.TCPConn); ok {
			_ = tcpConn.CloseWrite()
		}
		copyDone <- struct{}{}
	}()

	<-copyDone
	<-copyDone
}

func readSOCKS5Address(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		host := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(r, host); err != nil {
			return "", err
		}
		port, err := readSOCKS5Port(r)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(net.IP(host).String(), port), nil
	case 0x03:
		var hostLen [1]byte
		if _, err := io.ReadFull(r, hostLen[:]); err != nil {
			return "", err
		}
		host := make([]byte, int(hostLen[0]))
		if _, err := io.ReadFull(r, host); err != nil {
			return "", err
		}
		port, err := readSOCKS5Port(r)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(string(host), port), nil
	case 0x04:
		host := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(r, host); err != nil {
			return "", err
		}
		port, err := readSOCKS5Port(r)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(net.IP(host).String(), port), nil
	default:
		return "", io.ErrUnexpectedEOF
	}
}

func readSOCKS5Port(r io.Reader) (string, error) {
	var portBytes [2]byte
	if _, err := io.ReadFull(r, portBytes[:]); err != nil {
		return "", err
	}
	return strconv.Itoa(int(binary.BigEndian.Uint16(portBytes[:]))), nil
}
