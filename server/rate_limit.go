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
			l.evictLocked(now)
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

// evictionSampleSize is how many buckets are inspected when the table is full.
// Sampling keeps admission O(1) under a key-flooding attack; an exact LRU scan
// would make every new key cost O(maxKeys) while holding the shared mutex.
const evictionSampleSize = 8

// evictLocked drops the least recently used bucket among a small random sample.
// Go randomizes map iteration order, so the sample is unbiased. The caller must
// hold l.mu.
func (l *RateLimiter) evictLocked(now time.Time) {
	var victim string
	var oldest time.Time
	inspected := 0
	for candidate, bucket := range l.buckets {
		// An idle bucket has already refilled to full burst, so evicting it
		// grants the next caller nothing it would not have had anyway.
		if now.Sub(bucket.last) > 5*time.Minute {
			delete(l.buckets, candidate)
			return
		}
		if victim == "" || bucket.last.Before(oldest) {
			victim, oldest = candidate, bucket.last
		}
		if inspected++; inspected >= evictionSampleSize {
			break
		}
	}
	if victim != "" {
		delete(l.buckets, victim)
	}
}
