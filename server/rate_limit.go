package server

import (
	"sync"
	"time"
)

type rateBucket struct {
	tokens float64
	last   time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]rateBucket
	rate     float64
	burst    float64
	maxKeys  int
	lastTrim time.Time
}

func NewRateLimiter(rate, burst, maxKeys int) *RateLimiter {
	return &RateLimiter{buckets: make(map[string]rateBucket), rate: float64(rate), burst: float64(burst), maxKeys: maxKeys}
}

func (l *RateLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastTrim) >= time.Minute {
		for candidate, bucket := range l.buckets {
			if now.Sub(bucket.last) > 5*time.Minute {
				delete(l.buckets, candidate)
			}
		}
		l.lastTrim = now
	}
	bucket, exists := l.buckets[key]
	if !exists {
		if len(l.buckets) >= l.maxKeys {
			var oldestKey string
			var oldest time.Time
			for candidate, existing := range l.buckets {
				if oldestKey == "" || existing.last.Before(oldest) {
					oldestKey, oldest = candidate, existing.last
				}
			}
			delete(l.buckets, oldestKey)
		}
		bucket = rateBucket{tokens: l.burst, last: now}
	}
	elapsed := now.Sub(bucket.last).Seconds()
	bucket.tokens = min(l.burst, bucket.tokens+elapsed*l.rate)
	bucket.last = now
	if bucket.tokens < 1 {
		l.buckets[key] = bucket
		return false
	}
	bucket.tokens--
	l.buckets[key] = bucket
	return true
}
