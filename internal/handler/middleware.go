package handler

import (
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.New().String()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(wrapped, r)
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration", time.Since(start).String(),
		)
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// MaxBodySize limits request bodies to 1MB to prevent memory exhaustion.
func MaxBodySize(next http.Handler) http.Handler {
	const maxBytes = 1 << 20 // 1MB
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered", "error", err, "path", r.URL.Path)
				http.Error(w, `{"error":{"code":"INTERNAL","message":"internal server error"}}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RateLimiter provides per-IP rate limiting using a token bucket approach.
type RateLimiter struct {
	mu        sync.Mutex
	attempts  map[string][]time.Time
	limit     int
	window    time.Duration
	lastClean time.Time
}

// NewRateLimiter creates a rate limiter that allows `limit` requests per `window` per IP.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		attempts:  make(map[string][]time.Time),
		limit:     limit,
		window:    window,
		lastClean: time.Now(),
	}
}

// RateLimit wraps an http.HandlerFunc with per-IP rate limiting.
func (rl *RateLimiter) RateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}

		rl.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-rl.window)

		// Periodically evict stale IPs to bound memory
		if now.Sub(rl.lastClean) > 5*time.Minute {
			for k, times := range rl.attempts {
				if len(times) == 0 || times[len(times)-1].Before(cutoff) {
					delete(rl.attempts, k)
				}
			}
			rl.lastClean = now
		}

		// Prune old attempts for this IP
		recent := rl.attempts[ip][:0]
		for _, t := range rl.attempts[ip] {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}

		if len(recent) >= rl.limit {
			rl.mu.Unlock()
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts, try again later")
			return
		}

		rl.attempts[ip] = append(recent, now)
		rl.mu.Unlock()

		next(w, r)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
