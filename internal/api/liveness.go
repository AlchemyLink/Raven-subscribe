package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// Subscription liveness: every successful subscription fetch is a "this
// user's client is alive and can reach us" ping. Combined with per-user
// traffic from xray-stats-exporter it gives the one signal no probe can:
// "client refreshed its subscription, yet moved zero bytes" = the tunnel
// is dead for that user (client-version break, per-ISP ban of the relay,
// stale config after a rotation).
//
// Recorded by subscriptionMiddleware (router-level, after mux matched the
// route so the {token} var is known), exported on the private metrics
// listener as raven_sub_last_fetch_timestamp_seconds{email,path,client}.
// email is pseudonymised exactly like xray-stats-exporter does it
// (sha256(secret + ":" + email)[:8] hex) so the two series join in PromQL.
// State is in memory only; a restart forgets fetches until clients refresh.

type fetchKey struct {
	token    string // primary or fallback token as it appeared in the URL
	fallback bool
	client   string
}

type fetchTracker struct {
	mu   sync.Mutex
	last map[fetchKey]time.Time
}

func (t *fetchTracker) record(k fetchKey, at time.Time) {
	t.mu.Lock()
	if t.last == nil {
		t.last = map[fetchKey]time.Time{}
	}
	t.last[k] = at
	t.mu.Unlock()
}

func (t *fetchTracker) snapshot() map[fetchKey]time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[fetchKey]time.Time, len(t.last))
	for k, v := range t.last {
		out[k] = v
	}
	return out
}

// forget drops entries older than maxAge so rotated tokens do not linger.
func (t *fetchTracker) forget(maxAge time.Duration, now time.Time) {
	t.mu.Lock()
	for k, v := range t.last {
		if now.Sub(v) > maxAge {
			delete(t.last, k)
		}
	}
	t.mu.Unlock()
}

// clientFamily maps a User-Agent to a small fixed set so the client label
// stays low-cardinality and carries no version / device detail.
func clientFamily(ua string) string {
	u := strings.ToLower(ua)
	switch {
	case u == "":
		return "none"
	case strings.Contains(u, "happ"):
		return "happ"
	case strings.Contains(u, "v2rayng"):
		return "v2rayng"
	case strings.Contains(u, "v2rayn"):
		return "v2rayn"
	case strings.Contains(u, "hiddify"):
		return "hiddify"
	case strings.Contains(u, "v2box"):
		return "v2box"
	case strings.Contains(u, "streisand"):
		return "streisand"
	case strings.Contains(u, "shadowrocket"):
		return "shadowrocket"
	case strings.Contains(u, "nekobox"), strings.Contains(u, "nekoray"):
		return "nekobox"
	case strings.Contains(u, "karing"):
		return "karing"
	case strings.Contains(u, "sing-box"), strings.Contains(u, "sfa"), strings.Contains(u, "sfi"):
		return "singbox"
	case strings.Contains(u, "clash"), strings.Contains(u, "mihomo"), strings.Contains(u, "stash"):
		return "clash"
	case strings.Contains(u, "incy"):
		return "incy"
	case strings.Contains(u, "mozilla"), strings.Contains(u, "curl"), strings.Contains(u, "wget"):
		return "browser"
	}
	return "other"
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// isSubscriptionRoute reports whether the matched route template serves a
// subscription body to a client (not the hysteria auth hook, not admin).
func isSubscriptionRoute(tpl string) (ok, fallback bool) {
	switch {
	case strings.HasPrefix(tpl, "/sub/fallback/"), strings.HasPrefix(tpl, "/c/fallback/"):
		return true, true
	case strings.HasPrefix(tpl, "/sub/{token}"), strings.HasPrefix(tpl, "/c/{token}"):
		return true, false
	}
	return false, false
}

// subscriptionMiddleware sets Profile-Update-Interval on subscription
// responses (so clients refresh on a predictable cadence and the fetch
// timestamp becomes a liveness signal) and records successful fetches.
func (s *Server) subscriptionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := mux.CurrentRoute(r)
		if route == nil {
			next.ServeHTTP(w, r)
			return
		}
		tpl, _ := route.GetPathTemplate()
		isSub, fallback := isSubscriptionRoute(tpl)
		if !isSub {
			next.ServeHTTP(w, r)
			return
		}
		if h := s.cfg.ProfileUpdateIntervalHours; h > 0 {
			w.Header().Set("Profile-Update-Interval", strconv.Itoa(h))
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status/100 == 2 {
			if tok := mux.Vars(r)["token"]; tok != "" {
				s.fetches.record(fetchKey{token: tok, fallback: fallback, client: clientFamily(r.UserAgent())}, time.Now())
			}
		}
	})
}

// obscureEmail must stay byte-identical to xray-stats-exporter's version.
func obscureEmail(secret, email string) string {
	if secret == "" || email == "" {
		return email
	}
	sum := sha256.Sum256([]byte(secret + ":" + email))
	return hex.EncodeToString(sum[:8])
}

const fetchForgetAfter = 14 * 24 * time.Hour

// writeLivenessMetrics appends raven_sub_last_fetch_timestamp_seconds. Tokens
// are resolved to users at scrape time (≈30 users, a handful of indexed
// lookups) so the map never stores identities, and rotated / deleted tokens
// simply stop resolving.
func (s *Server) writeLivenessMetrics(w io.Writer) {
	now := time.Now()
	s.fetches.forget(fetchForgetAfter, now)
	snap := s.fetches.snapshot()

	type row struct {
		email, path, client string
		ts                  int64
	}
	best := map[string]row{} // email|path|client -> newest
	for k, at := range snap {
		var email string
		if k.fallback {
			if u, err := s.db.GetUserByFallbackToken(k.token); err == nil && u != nil {
				email = u.ClientIdentity()
			}
		} else if u, err := s.db.GetUserByToken(k.token); err == nil && u != nil {
			email = u.ClientIdentity()
		}
		if email == "" {
			continue
		}
		path := "primary"
		if k.fallback {
			path = "fallback"
		}
		e := obscureEmail(s.cfg.MetricsAnonymizeSecret, email)
		id := e + "|" + path + "|" + k.client
		if cur, ok := best[id]; !ok || at.Unix() > cur.ts {
			best[id] = row{email: e, path: path, client: k.client, ts: at.Unix()}
		}
	}
	ids := make([]string, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	_, _ = fmt.Fprint(w, "# HELP raven_sub_last_fetch_timestamp_seconds Unix time of the last successful subscription fetch per user, path (primary|fallback) and client family. email is pseudonymised like xray-stats-exporter. In-memory: resets on restart.\n")
	_, _ = fmt.Fprint(w, "# TYPE raven_sub_last_fetch_timestamp_seconds gauge\n")
	for _, id := range ids {
		r := best[id]
		_, _ = fmt.Fprintf(w, "raven_sub_last_fetch_timestamp_seconds{email=%q,path=%q,client=%q} %d\n", r.email, r.path, r.client, r.ts)
	}
	_, _ = fmt.Fprint(w, "# HELP raven_sub_profile_update_interval_hours Profile-Update-Interval advertised to clients (0 = header not sent).\n")
	_, _ = fmt.Fprint(w, "# TYPE raven_sub_profile_update_interval_hours gauge\n")
	_, _ = fmt.Fprintf(w, "raven_sub_profile_update_interval_hours %d\n", s.cfg.ProfileUpdateIntervalHours)
}
