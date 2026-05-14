package prowlarr

import (
	"context"
	"sync"
	"time"
)

// rateLimiter enforces a per-request minimum interval so LongBox is polite to
// the local Prowlarr instance. Unlike the Metron limiter this has no quota
// windows — Prowlarr is self-hosted and has no hard rate limits. We just avoid
// hammering it with back-to-back requests.
type rateLimiter struct {
	mu          sync.Mutex
	minInterval time.Duration
	lastRequest time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		minInterval: 250 * time.Millisecond,
	}
}

// wait blocks until the min-interval since the last request has elapsed, then
// records the new request time. Returns ctx.Err() if the context is cancelled
// while waiting.
func (rl *rateLimiter) wait(ctx context.Context) error {
	for {
		rl.mu.Lock()
		now := time.Now()
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
		rl.lastRequest = time.Now()
		rl.mu.Unlock()
		return nil
	}
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
