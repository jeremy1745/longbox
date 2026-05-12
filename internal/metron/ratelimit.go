package metron

import (
	"sync"
	"time"
)

// RateLimiter enforces Metron's two-tier rate limit:
//   - 20 requests per minute (burst)
//   - 5000 requests per day (sustained)
//
// Both are tracked via sliding windows. The minute window also enforces
// a soft pacing interval (~3 seconds) so a burst of 20 doesn't all hit
// in the first second of a window.
type RateLimiter struct {
	mu sync.Mutex

	minInterval time.Duration

	burstMax    int
	burstStart  time.Time
	burstCount  int

	dailyMax   int
	dailyStart time.Time
	dailyCount int

	lastRequest time.Time
}

func NewRateLimiter() *RateLimiter {
	now := time.Now()
	return &RateLimiter{
		minInterval: 3 * time.Second, // 20/min ≈ one every 3s with margin
		burstMax:    18,              // 20 - 2 for safety
		burstStart:  now,
		dailyMax:    4800, // 5000 - 200 for safety
		dailyStart:  now,
	}
}

// Wait blocks until it's safe to make the next request. Sleeps the
// calling goroutine; never returns an error.
func (rl *RateLimiter) Wait() {
	for {
		rl.mu.Lock()
		now := time.Now()

		// Roll windows that have elapsed.
		if now.Sub(rl.burstStart) >= time.Minute {
			rl.burstStart = now
			rl.burstCount = 0
		}
		if now.Sub(rl.dailyStart) >= 24*time.Hour {
			rl.dailyStart = now
			rl.dailyCount = 0
		}

		// Burst limit.
		if rl.burstCount >= rl.burstMax {
			wait := rl.burstStart.Add(time.Minute).Sub(now)
			rl.mu.Unlock()
			if wait > 0 {
				time.Sleep(wait)
			}
			continue
		}

		// Daily limit.
		if rl.dailyCount >= rl.dailyMax {
			wait := rl.dailyStart.Add(24 * time.Hour).Sub(now)
			rl.mu.Unlock()
			if wait > 0 {
				time.Sleep(wait)
			}
			continue
		}

		// Soft pacing — bound minimum interval between requests.
		if elapsed := now.Sub(rl.lastRequest); elapsed < rl.minInterval {
			wait := rl.minInterval - elapsed
			rl.mu.Unlock()
			time.Sleep(wait)
			continue
		}

		rl.burstCount++
		rl.dailyCount++
		rl.lastRequest = time.Now()
		rl.mu.Unlock()
		return
	}
}

// BurstRemaining returns how many requests can fire in the current
// minute window before the burst limit kicks in.
func (rl *RateLimiter) BurstRemaining() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if time.Since(rl.burstStart) >= time.Minute {
		return rl.burstMax
	}
	r := rl.burstMax - rl.burstCount
	if r < 0 {
		return 0
	}
	return r
}

// DailyRemaining returns how many requests are left in the current
// 24-hour window before the sustained cap kicks in.
func (rl *RateLimiter) DailyRemaining() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if time.Since(rl.dailyStart) >= 24*time.Hour {
		return rl.dailyMax
	}
	r := rl.dailyMax - rl.dailyCount
	if r < 0 {
		return 0
	}
	return r
}
