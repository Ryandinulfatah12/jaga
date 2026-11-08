// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package status

import (
	"slices"
	"testing"
	"time"
)

func TestHub(t *testing.T) {
	h := NewHub()
	now := time.Unix(1_800_000_000, 0)
	h.now = func() time.Time { return now }
	ch, cancel := h.Subscribe()

	h.Set(Status{CameraID: 2, State: Starting})
	h.Set(Status{CameraID: 1, State: Recording})
	now = now.Add(time.Minute)
	h.Set(Status{CameraID: 1, State: Recording}) // same state: Since kept
	if s, _ := h.Get(1); !s.Since.Equal(time.Unix(1_800_000_000, 0)) {
		t.Errorf("since moved on an unchanged state: %v", s.Since)
	}
	h.Set(Status{CameraID: 2, State: Retrying, Error: "refused", Failures: 1})
	if s, _ := h.Get(2); !s.Since.Equal(now) || s.Error != "refused" {
		t.Errorf("camera 2 = %+v", s)
	}
	if all := h.All(); len(all) != 2 || all[0].CameraID != 1 || all[1].CameraID != 2 {
		t.Errorf("all = %+v", all)
	}
	h.Set(Status{CameraID: 2, State: Stopped})
	if _, ok := h.Get(2); ok {
		t.Error("stopped camera still current")
	}
	var got []State
	for range 5 {
		got = append(got, (<-ch).State)
	}
	if want := []State{Starting, Recording, Recording, Retrying, Stopped}; !slices.Equal(got, want) {
		t.Errorf("subscriber got %v, want %v", got, want)
	}
	cancel()
	cancel() // twice is fine
	if _, open := <-ch; open {
		t.Error("channel open after cancel")
	}
}

func TestSlowSubscriberDropped(t *testing.T) {
	h := NewHub()
	slow, cancel := h.Subscribe()
	defer cancel()
	for i := range subBuffer + 10 {
		h.Set(Status{CameraID: int64(i), State: Recording}) // never blocks
	}
	n := 0
	for range slow { // closed once it fell behind
		n++
	}
	if n != subBuffer {
		t.Errorf("slow subscriber got %d before being dropped, want %d", n, subBuffer)
	}
	if len(h.All()) != subBuffer+10 {
		t.Error("state lost while a subscriber was dropped")
	}
}
