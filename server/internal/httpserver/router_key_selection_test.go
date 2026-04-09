package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"tavily-proxy/server/internal/db"
	"tavily-proxy/server/internal/services"
)

func newSettingsServiceTestDep(t *testing.T) *services.SettingsService {
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

	return services.NewSettingsService(database)
}

func TestHandleGetKeySelection_DefaultsToFillFirst(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	settings := newSettingsServiceTestDep(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/settings/key-selection", nil)

	handleGetKeySelection(c, settings)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusOK)
	}

	var out map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v (body=%q)", err, w.Body.String())
	}
	if out["policy"] != services.KeySelectionPolicyFillFirst {
		t.Fatalf("unexpected default policy: got %q want %q", out["policy"], services.KeySelectionPolicyFillFirst)
	}
}

func TestHandleSetKeySelection_PersistsBalancePolicy(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	settings := newSettingsServiceTestDep(t)

	body := bytes.NewBufferString(`{"policy":"balance"}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/settings/key-selection", body)
	c.Request.Header.Set("Content-Type", "application/json")

	handleSetKeySelection(c, settings)

	if got := c.Writer.Status(); got != http.StatusNoContent {
		t.Fatalf("unexpected status: got %d want %d body=%q", got, http.StatusNoContent, w.Body.String())
	}

	value, ok, err := settings.Get(context.Background(), services.SettingKeySelectionPolicy)
	if err != nil {
		t.Fatalf("read saved policy: %v", err)
	}
	if !ok {
		t.Fatalf("expected saved policy")
	}
	if value != services.KeySelectionPolicyBalance {
		t.Fatalf("unexpected saved policy: got %q want %q", value, services.KeySelectionPolicyBalance)
	}
}

func TestHandleSetKeySelection_RejectsInvalidPolicy(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	settings := newSettingsServiceTestDep(t)

	body := bytes.NewBufferString(`{"policy":"unknown"}`)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/settings/key-selection", body)
	c.Request.Header.Set("Content-Type", "application/json")

	handleSetKeySelection(c, settings)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d want %d body=%q", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var out map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v (body=%q)", err, w.Body.String())
	}
	if out["error"] != "invalid_policy" {
		t.Fatalf("unexpected error: got %q want %q", out["error"], "invalid_policy")
	}
}
