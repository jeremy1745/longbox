package comicvine

import (
	"context"
	"sync"
	"time"
)

// RateLimiter enforces ComicVine API rate limits:
// - Minimum 1 second between requests
// - Maximum 200 requests per hour (tracked via sliding window)
type RateLimiter struct {
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration
	hourlyMax   int
	hourlyCount int
	hourStart   time.Time
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		minInterval: 1100 * time.Millisecond, // slightly over 1s for safety
		hourlyMax:   190,                       // leave some headroom under 200
		hourStart:   time.Now(),
	}
}

// Wait blocks until it's safe to make the next request, or until ctx is done.
// Returns ctx.Err() if cancelled mid-wait. Honors both the per-request minimum
// interval and the hourly cap — when the hourly cap is exhausted it can sleep
// up to ~1 hour, but cancellation aborts immediately.
func (rl *RateLimiter) Wait(ctx context.Context) error {
	for {
		rl.mu.Lock()
		now := time.Now()

		// Reset hourly counter if the hour has rolled over.
		if now.Sub(rl.hourStart) >= time.Hour {
			rl.hourlyCount = 0
			rl.hourStart = now
		}

		// Hourly cap exhausted — release lock and wait for the window reset.
		if rl.hourlyCount >= rl.hourlyMax {
			waitUntil := rl.hourStart.Add(time.Hour)
			sleepTime := waitUntil.Sub(now)
			rl.mu.Unlock()
			if sleepTime <= 0 {
				continue
			}
			if err := sleepCtx(ctx, sleepTime); err != nil {
				return err
			}
			continue
		}

		// Enforce minimum interval between consecutive requests.
		elapsed := now.Sub(rl.lastRequest)
		if elapsed < rl.minInterval {
			sleepTime := rl.minInterval - elapsed
			rl.mu.Unlock()
			if err := sleepCtx(ctx, sleepTime); err != nil {
				return err
			}
			continue
		}

		// Reserve our slot.
		rl.lastRequest = time.Now()
		rl.hourlyCount++
		rl.mu.Unlock()
		return nil
	}
}

// sleepCtx is a context-aware time.Sleep — returns ctx.Err() on cancel.
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

// NextResetIn reports the remaining time until the hourly quota window
// resets. Useful for surfacing wait ETAs in the UI when callers detect
// HourlyRemaining() == 0.
func (rl *RateLimiter) NextResetIn() time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	d := time.Until(rl.hourStart.Add(time.Hour))
	if d < 0 {
		return 0
	}
	return d
}

// HourlyRemaining returns how many requests are left in the current hourly window.
func (rl *RateLimiter) HourlyRemaining() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if time.Since(rl.hourStart) >= time.Hour {
		return rl.hourlyMax
	}
	remaining := rl.hourlyMax - rl.hourlyCount
	if remaining < 0 {
		return 0
	}
	return remaining
}
