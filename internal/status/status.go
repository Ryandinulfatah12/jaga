// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package status keeps the current recorder state of every camera and fans
// changes out to subscribers (the SSE stream).
package status

import (
	"slices"
	"sync"
	"time"
)

// State of a camera's recorder.
type State string

// States. A camera that is disabled or not set to record has no status at
// all; the API reports it as "idle".
const (
	Starting  State = "starting"  // FFmpeg spawned, no frames yet
	Recording State = "recording" // frames arriving
	Retrying  State = "retrying"  // failed, waiting for the backoff
	Offline   State = "offline"   // failed offlineAfter times in a row (an offline event is open)
	Stopped   State = "stopped"   // recorder ended: camera removed, disabled or changed
)

// Status is one camera's state. Since is when the state last changed, so
// for Recording it is the uptime start.
type Status struct {
	CameraID int64     `json:"camera_id"`
	State    State     `json:"state"`
	Since    time.Time `json:"since,omitzero"`  // absent for idle
	Error    string    `json:"error,omitempty"` // last failure, credentials masked
	Failures int       `json:"failures,omitempty"`
}

// Hub is safe for concurrent use. The zero value is not usable; use NewHub.
type Hub struct {
	mu   sync.Mutex
	cur  map[int64]Status
	subs map[chan Status]struct{}
	now  func() time.Time
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{cur: map[int64]Status{}, subs: map[chan Status]struct{}{}, now: time.Now}
}

// subBuffer is how many changes a subscriber may fall behind before it is
// dropped. The SSE client then reconnects and starts from a fresh snapshot.
const subBuffer = 64

// Set records a camera's state and notifies subscribers. Since is filled
// in: now when the state changed, kept when it did not. Stopped removes the
// camera from the current set after notifying.
func (h *Hub) Set(s Status) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.cur[s.CameraID]; ok && prev.State == s.State {
		s.Since = prev.Since
	} else {
		s.Since = h.now().UTC()
	}
	if s.State == Stopped {
		delete(h.cur, s.CameraID)
	} else {
		h.cur[s.CameraID] = s
	}
	for ch := range h.subs {
		select {
		case ch <- s:
		default: // too slow: drop it rather than block the recorders
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// Get returns a camera's state; ok is false when it is not recording.
func (h *Hub) Get(id int64) (Status, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.cur[id]
	return s, ok
}

// All returns every current state, ordered by camera id.
func (h *Hub) All() []Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Status, 0, len(h.cur))
	for _, s := range h.cur {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Status) int { return int(a.CameraID - b.CameraID) })
	return out
}

// Subscribe returns a channel of changes. It is closed when the subscriber
// falls behind or after cancel. Subscribe before reading All so no change
// is missed between the two.
func (h *Hub) Subscribe() (<-chan Status, func()) {
	ch := make(chan Status, subBuffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	}
}
