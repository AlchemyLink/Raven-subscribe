package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetrics_FallbackEnabledGauge(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	if err := srv.db.SetFallbackEnabled(true); err != nil {
		t.Fatalf("SetFallbackEnabled: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.MetricsHandler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "raven_fallback_enabled 1") {
		t.Errorf("enabled=true: body should contain raven_fallback_enabled 1, got:\n%s", rec.Body.String())
	}

	if err := srv.db.SetFallbackEnabled(false); err != nil {
		t.Fatalf("SetFallbackEnabled: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.MetricsHandler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "raven_fallback_enabled 0") {
		t.Errorf("enabled=false: body should contain raven_fallback_enabled 0, got:\n%s", rec.Body.String())
	}
}

func TestMetrics_FallbackDeniedCounterIncrements(t *testing.T) {
	srv, cleanup := testServer(t)
	defer cleanup()

	if err := srv.db.SetFallbackEnabled(false); err != nil {
		t.Fatalf("SetFallbackEnabled: %v", err)
	}

	router := srv.Router()
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/fallback/sometoken", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("fallback request %d: status = %d, want 403", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	srv.MetricsHandler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "raven_fallback_denied_total 3") {
		t.Errorf("body should contain raven_fallback_denied_total 3, got:\n%s", rec.Body.String())
	}

	// Enabled fallback with an unknown token is a 404, not a denial — counter must not move.
	if err := srv.db.SetFallbackEnabled(true); err != nil {
		t.Fatalf("SetFallbackEnabled: %v", err)
	}
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/fallback/sometoken", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("enabled+unknown token: status = %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.MetricsHandler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "raven_fallback_denied_total 3") {
		t.Errorf("counter must stay at 3 after non-denial, got:\n%s", rec.Body.String())
	}
}
