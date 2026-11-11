// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"path/filepath"
	"strings"
	"testing"
)

// recordingPath guards every file served or deleted by segment ID: whatever
// the DB holds, the result must stay inside data_dir/recordings.
func FuzzRecordingPath(f *testing.F) {
	for _, s := range []string{"recordings/1/2026-11-08/a.mp4", "recordings/../../etc/passwd", "/etc/passwd",
		"recordings/", "recordings//x", "recordings/a/../../b", "recordings\\..\\x", "recordings/1/a\nfile 'x'"} {
		f.Add(s)
	}
	root := filepath.Join(f.TempDir(), "data")
	f.Fuzz(func(t *testing.T, rel string) {
		p, ok := recordingPath(root, rel)
		if !ok {
			return
		}
		in, err := filepath.Rel(filepath.Join(root, "recordings"), p)
		// "." is recordings/ itself: not an escape (opening it as a file fails).
		if err != nil || in == ".." || strings.HasPrefix(in, ".."+string(filepath.Separator)) || filepath.IsAbs(in) {
			t.Fatalf("recordingPath(%q) = %q escapes recordings/", rel, p)
		}
	})
}
