package comicvine

import (
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

// Wait blocks until it's safe to make the next request.
// Returns an error if the hourly limit has been reached.
func (rl *RateLimiter) Wait() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Reset hourly counter if the hour has rolled over
	if now.Sub(rl.hourStart) >= time.Hour {
		rl.hourlyCount = 0
		rl.hourStart = now
	}

	// If we've hit the hourly limit, wait until the window resets
	if rl.hourlyCount >= rl.hourlyMax {
		waitUntil := rl.hourStart.Add(time.Hour)
		sleepTime := waitUntil.Sub(now)
		if sleepTime > 0 {
			rl.mu.Unlock()
			time.Sleep(sleepTime)
			rl.mu.Lock()
			rl.hourlyCount = 0
			rl.hourStart = time.Now()
		}
	}

	// Enforce minimum interval between requests
	elapsed := now.Sub(rl.lastRequest)
	if elapsed < rl.minInterval {
		sleepTime := rl.minInterval - elapsed
		rl.mu.Unlock()
		time.Sleep(sleepTime)
		rl.mu.Lock()
	}

	rl.lastRequest = time.Now()
	rl.hourlyCount++
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
