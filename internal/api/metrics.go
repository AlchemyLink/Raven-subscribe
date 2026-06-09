package api

import (
	"fmt"
	"log"
	"net/http"
)

// MetricsHandler serves a minimal Prometheus text-format exposition endpoint.
// Hand-rolled on purpose: two series do not justify a client_golang dependency.
//
// Serve this on a SEPARATE listener (config metrics_listen), never on the main
// router — the main router is publicly reachable through the subscription vhost.
//
// raven_fallback_enabled directly encodes the Fallback Model A invariant
// (enabled=1 is the steady state); monitoring alerts when it stays 0 outside a
// sanitization window. raven_fallback_denied_total counts user-facing 403s from
// the disabled fallback, i.e. real client impact.
func (s *Server) MetricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		enabled, err := s.db.GetFallbackEnabled()
		if err != nil {
			log.Printf("ERROR metrics: get fallback enabled: %v", err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		enabledVal := 0
		if enabled {
			enabledVal = 1
		}
		body := fmt.Sprintf(
			"# HELP raven_fallback_enabled Global fallback subscription flag (1 = enabled, the Model A steady state).\n"+
				"# TYPE raven_fallback_enabled gauge\n"+
				"raven_fallback_enabled %d\n"+
				"# HELP raven_fallback_denied_total Fallback subscription requests rejected with 403 because the global flag is off. Resets on restart.\n"+
				"# TYPE raven_fallback_denied_total counter\n"+
				"raven_fallback_denied_total %d\n",
			enabledVal, s.fallbackDenied.Load())
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}
