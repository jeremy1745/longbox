package newznab

import (
	"sync"
	"time"
)

// RateLimiter enforces a minimum interval between requests.
type RateLimiter struct {
	mu          sync.Mutex
	lastRequest time.Time
	minInterval time.Duration
}

// NewRateLimiter creates a rate limiter that allows at most maxPerSec requests per second.
func NewRateLimiter(maxPerSec int) *RateLimiter {
	interval := time.Second / time.Duration(maxPerSec)
	return &RateLimiter{
		minInterval: interval,
	}
}

// Wait blocks until the next request is allowed.
func (rl *RateLimiter) Wait() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRequest)
	if elapsed < rl.minInterval {
		time.Sleep(rl.minInterval - elapsed)
	}
	rl.lastRequest = time.Now()
}
