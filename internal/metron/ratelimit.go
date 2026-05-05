package metron

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimiter tracks Metron's two-window quota — 20/min burst, 5000/day
// sustained — by reading the X-RateLimit-* headers from every response.
// Wait blocks before each request when either window is exhausted, until
// the corresponding reset time. Also enforces a per-request minimum
// interval (Metron's DRF throttle smooths bursts to ~1 req/sec).
type rateLimiter struct {
	mu sync.Mutex

	// Last-known counters, updated on each successful response.
	burstLimit         int
	burstRemaining     int
	burstResetAt       time.Time
	sustainedLimit     int
	sustainedRemaining int
	sustainedResetAt   time.Time

	// Floor values used before we've made our first request.
	defaultBurstMax     int
	defaultSustainedMax int

	// Min interval between consecutive requests. Metron's DRF stack
	// throttles back-to-back calls even when the per-minute counter is
	// fine, so we pace at slightly over 1s.
	minInterval time.Duration
	lastRequest time.Time

	// Until at least this time, do not send a request. Set when we observe
	// a 429 with a Retry-After header (defensive — the prior-pause logic
	// should make this rare).
	pauseUntil time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		defaultBurstMax:     20,
		defaultSustainedMax: 5000,
		minInterval:         1100 * time.Millisecond,
	}
}

// wait blocks until it's safe to send another request, or returns ctx.Err()
// if cancelled. Honors observed remaining counters and any pauseUntil from
// a recent 429.
func (rl *rateLimiter) wait(ctx context.Context) error {
	for {
		rl.mu.Lock()
		now := time.Now()

		// If a pauseUntil window is set (e.g. from a Retry-After), wait it out.
		if !rl.pauseUntil.IsZero() && rl.pauseUntil.After(now) {
			d := rl.pauseUntil.Sub(now)
			rl.mu.Unlock()
			if err := sleepCtx(ctx, d); err != nil {
				return err
			}
			continue
		}

		// Reset window-counters that have rolled over since we last observed
		// them so we don't pause forever on stale data.
		if !rl.burstResetAt.IsZero() && now.After(rl.burstResetAt) {
			rl.burstRemaining = rl.burstLimit
		}
		if !rl.sustainedResetAt.IsZero() && now.After(rl.sustainedResetAt) {
			rl.sustainedRemaining = rl.sustainedLimit
		}

		// Burst window exhausted — wait for reset.
		if rl.burstLimit > 0 && rl.burstRemaining <= 0 && !rl.burstResetAt.IsZero() && rl.burstResetAt.After(now) {
			d := rl.burstResetAt.Sub(now)
			rl.mu.Unlock()
			if err := sleepCtx(ctx, d); err != nil {
				return err
			}
			continue
		}

		// Sustained (daily) window exhausted — wait for reset. This can be
		// hours; cancellation must work.
		if rl.sustainedLimit > 0 && rl.sustainedRemaining <= 0 && !rl.sustainedResetAt.IsZero() && rl.sustainedResetAt.After(now) {
			d := rl.sustainedResetAt.Sub(now)
			rl.mu.Unlock()
			if err := sleepCtx(ctx, d); err != nil {
				return err
			}
			continue
		}

		// Min-interval gate: ensure ≥ minInterval since the last request.
		if rl.minInterval > 0 && !rl.lastRequest.IsZero() {
			elapsed := now.Sub(rl.lastRequest)
			if elapsed < rl.minInterval {
				d := rl.minInterval - elapsed
				rl.mu.Unlock()
				if err := sleepCtx(ctx, d); err != nil {
					return err
				}
				continue
			}
		}

		// Reserve a slot optimistically; the real value is corrected when
		// the response headers come back in observe().
		if rl.burstLimit > 0 {
			rl.burstRemaining--
		}
		if rl.sustainedLimit > 0 {
			rl.sustainedRemaining--
		}
		rl.lastRequest = time.Now()
		rl.mu.Unlock()
		return nil
	}
}

// observe folds the X-RateLimit-* response headers into the limiter so the
// next wait() reflects ground truth.
func (rl *rateLimiter) observe(headers http.Header) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if v, ok := readIntHeader(headers, "X-RateLimit-Burst-Limit"); ok {
		rl.burstLimit = v
	}
	if v, ok := readIntHeader(headers, "X-RateLimit-Burst-Remaining"); ok {
		rl.burstRemaining = v
	}
	if t, ok := readTimeHeader(headers, "X-RateLimit-Burst-Reset"); ok {
		rl.burstResetAt = t
	}
	if v, ok := readIntHeader(headers, "X-RateLimit-Sustained-Limit"); ok {
		rl.sustainedLimit = v
	}
	if v, ok := readIntHeader(headers, "X-RateLimit-Sustained-Remaining"); ok {
		rl.sustainedRemaining = v
	}
	if t, ok := readTimeHeader(headers, "X-RateLimit-Sustained-Reset"); ok {
		rl.sustainedResetAt = t
	}
}

// observeRetryAfter sets a hard pause until the server's stated retry time.
// Called when we hit a 429 anyway (e.g., race with another client).
func (rl *rateLimiter) observeRetryAfter(headers http.Header) {
	if ra := headers.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			rl.mu.Lock()
			rl.pauseUntil = time.Now().Add(time.Duration(secs) * time.Second)
			rl.mu.Unlock()
		}
	}
}

// snapshot returns a stable view of the current counters.
func (rl *rateLimiter) snapshot() QuotaSnapshot {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	s := QuotaSnapshot{
		BurstLimit:         rl.burstLimit,
		BurstRemaining:     rl.burstRemaining,
		SustainedLimit:     rl.sustainedLimit,
		SustainedRemaining: rl.sustainedRemaining,
	}
	if !rl.burstResetAt.IsZero() {
		s.BurstResetUnix = rl.burstResetAt.Unix()
	}
	if !rl.sustainedResetAt.IsZero() {
		s.SustainedResetUnix = rl.sustainedResetAt.Unix()
	}
	s.LastObservationUnix = time.Now().Unix()
	return s
}

func readIntHeader(h http.Header, name string) (int, bool) {
	v := h.Get(name)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// readTimeHeader accepts both an epoch second integer and an RFC3339 string.
// Metron's docs show the X-RateLimit-*-Reset value as a UNIX timestamp; some
// reverse proxies rewrite it to seconds-from-now. We try absolute first,
// then fall back to "seconds from now."
func readTimeHeader(h http.Header, name string) (time.Time, bool) {
	v := h.Get(name)
	if v == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	// Heuristic: timestamps far above current epoch are absolute; small
	// values are seconds-from-now.
	now := time.Now()
	if n > now.Unix()-365*24*3600 && n < now.Unix()+365*24*3600 {
		return time.Unix(n, 0), true
	}
	return now.Add(time.Duration(n) * time.Second), true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
