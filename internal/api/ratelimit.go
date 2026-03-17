package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipLimiter holds a rate.Limiter per IP with optional cleanup.
type ipLimiter struct {
	limiter *rate.Limiter
	last    time.Time
}

// rateLimitMiddleware returns a middleware that limits requests per IP.
// perMin: max requests per minute per IP; 0 = no limit.
func rateLimitMiddleware(perMin int) func(http.Handler) http.Handler {
	if perMin <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	// rate: perMin/60 per second, burst: min(perMin/6, 20)
	interval := time.Minute / time.Duration(perMin)
	burst := perMin / 6
	if burst < 2 {
		burst = 2
	}
	if burst > 20 {
		burst = 20
	}

	var (
		limiters = make(map[string]*ipLimiter)
		mu       sync.Mutex
	)

	// Cleanup stale limiters every 5 minutes.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			now := time.Now()
			for ip, il := range limiters {
				if now.Sub(il.last) > 10*time.Minute {
					delete(limiters, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			mu.Lock()
			il, ok := limiters[ip]
			if !ok {
				il = &ipLimiter{
					limiter: rate.NewLimiter(rate.Every(interval), burst),
					last:   time.Now(),
				}
				limiters[ip] = il
			}
			il.last = time.Now()
			l := il.limiter
			mu.Unlock()

			if !l.Allow() {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First IP is the client when behind a proxy.
		for i, c := range xff {
			if c == ',' || c == ' ' {
				return strings.TrimSpace(xff[:i])
			}
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return r.RemoteAddr
}
