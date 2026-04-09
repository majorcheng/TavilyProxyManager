package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"tavily-proxy/server/internal/services"
)

func TestHandleGetUpstreamProxy_DefaultsDisabled(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	settings := newSettingsServiceTestDep(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/settings/upstream-proxy", nil)

	handleGetUpstreamProxy(c, settings)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}

	var out struct {
		Enabled  bool   `json:"enabled"`
		ProxyURL string `json:"proxy_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v (body=%q)", err, w.Body.String())
	}
	if out.Enabled {
		t.Fatalf("expected proxy disabled by default")
	}
	if out.ProxyURL != "" {
		t.Fatalf("unexpected default proxy url: %q", out.ProxyURL)
	}
}

func TestHandleSetUpstreamProxy_PersistsConfig(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	settings := newSettingsServiceTestDep(t)

	body := bytes.NewBufferString(`{"enabled":true,"proxy_url":"socks5://127.0.0.1:1080"}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/settings/upstream-proxy", body)
	c.Request.Header.Set("Content-Type", "application/json")

	handleSetUpstreamProxy(c, settings)

	if got := c.Writer.Status(); got != http.StatusNoContent {
		t.Fatalf("unexpected status: got %d want %d body=%q", got, http.StatusNoContent, w.Body.String())
	}

	enabled, err := settings.GetBool(context.Background(), services.SettingUpstreamProxyEnabled, false)
	if err != nil {
		t.Fatalf("read saved enabled: %v", err)
	}
	if !enabled {
		t.Fatalf("expected proxy enabled")
	}

	value, ok, err := settings.Get(context.Background(), services.SettingUpstreamProxyURL)
	if err != nil {
		t.Fatalf("read saved proxy url: %v", err)
	}
	if !ok {
		t.Fatalf("expected saved proxy url")
	}
	if value != "socks5://127.0.0.1:1080" {
		t.Fatalf("unexpected saved proxy url: got %q want %q", value, "socks5://127.0.0.1:1080")
	}
}

func TestHandleSetUpstreamProxy_RejectsInvalidProxyURL(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	settings := newSettingsServiceTestDep(t)

	body := bytes.NewBufferString(`{"enabled":true,"proxy_url":"ftp://127.0.0.1:21"}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/settings/upstream-proxy", body)
	c.Request.Header.Set("Content-Type", "application/json")

	handleSetUpstreamProxy(c, settings)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d want %d body=%q", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var out map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v (body=%q)", err, w.Body.String())
	}
	if out["error"] != "invalid_proxy_url" {
		t.Fatalf("unexpected error: got %q want %q", out["error"], "invalid_proxy_url")
	}
}
