// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/status"
)

// idle is the status of a camera that has no recorder (disabled, record_mode
// off, or just created and not started yet).
func idle(id int64) status.Status { return status.Status{CameraID: id, State: "idle"} }

// cameraStatus answers GET /cameras/{id}/status.
func (s *Server) cameraStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.Cameras.Get(r.Context(), id); err != nil {
		s.cameraError(w, r, err)
		return
	}
	st, ok := s.Status.Get(id)
	if !ok {
		st = idle(id)
	}
	writeJSON(w, http.StatusOK, st)
}

// heartbeat keeps idle SSE connections open through proxies and lets the
// server notice clients that went away.
var heartbeat = 25 * time.Second

// eventStream is a Server-Sent Events stream: first a "snapshot" event with
// every camera's status, then a "status" event per change. EventSource
// reconnects on its own; every connection starts with a fresh snapshot, so
// no event IDs or replay are needed.
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	// The server's WriteTimeout (if any) must not cut a long-lived stream.
	_ = rc.SetWriteDeadline(time.Time{})

	updates, cancel := s.Status.Subscribe() // before All, so nothing is missed
	defer cancel()

	all, err := s.Cameras.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	snap := make([]status.Status, 0, len(all))
	for _, c := range all {
		st, ok := s.Status.Get(c.ID)
		if !ok {
			st = idle(c.ID)
		}
		snap = append(snap, st)
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
	w.WriteHeader(http.StatusOK)

	send := func(event string, v any) bool {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send("snapshot", snap) {
		return
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case u, open := <-updates:
			if !open {
				return // fell behind: the client reconnects and gets a new snapshot
			}
			if u.State == status.Stopped {
				u = idle(u.CameraID)
				u.Since = time.Now().UTC()
			}
			if !send("status", u) {
				return
			}
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
