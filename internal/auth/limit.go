// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package auth

import (
	"sync"
	"time"
)

// Limiter allows a fixed number of attempts per key (client IP) per window.
// ponytail: in memory, so limits reset on restart and are per process;
// enough for a single-binary NVR. Move to the DB if Jaga ever runs replicated.
type Limiter struct {
	Max    int
	Window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	hits map[string][]time.Time
}

// NewLimiter allows max attempts per window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, now: time.Now, hits: map[string][]time.Time{}}
}

// Allow records an attempt for key and reports whether it is within the limit.
// It also returns how long until the next attempt would be allowed.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.Window)
	// Drop old hits for every key now and then so the map cannot grow forever.
	if len(l.hits) > 10_000 {
		for k, ts := range l.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	ts := l.hits[key]
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	ts = ts[i:]
	if len(ts) >= l.Max {
		l.hits[key] = ts
		return false, ts[0].Add(l.Window).Sub(now)
	}
	l.hits[key] = append(ts, now)
	return true, 0
}
