// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package retention

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// file writes a recording that is not indexed, modified age before now.
func (f *fixture) file(cam string, name string, age time.Duration, body string) string {
	f.t.Helper()
	rel := filepath.Join("recordings", cam, "2026-11-20", name)
	abs := filepath.Join(f.j.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		f.t.Fatal(err)
	}
	mt := now.Add(-age)
	if err := os.Chtimes(abs, mt, mt); err != nil {
		f.t.Fatal(err)
	}
	return rel
}

func (f *fixture) onDisk(rel string) bool {
	_, err := os.Stat(filepath.Join(f.j.DataDir, rel))
	return err == nil
}

func (f *fixture) row(rel string) (start, end, size int64, ok bool) {
	err := f.j.DB.QueryRowContext(context.Background(),
		`SELECT start_ts, end_ts, size_bytes FROM segments WHERE path = ?`, filepath.ToSlash(rel)).Scan(&start, &end, &size)
	return start, end, size, err == nil
}

func TestReconcile(t *testing.T) {
	f := setup(t)
	cam := f.camera(nil)
	camDir := itoa(cam)
	// "corrupt" files fail to probe, like a segment cut by SIGKILL (moov atom not found).
	f.j.Probe = func(_ context.Context, path string) (time.Duration, error) {
		b, _ := os.ReadFile(path) //nolint:gosec // test file
		if string(b) == "corrupt" {
			return 0, errors.New("moov atom not found")
		}
		return 7 * time.Second, nil
	}

	// Names differ from the indexed segments below (they end 10:00 and 11:00).
	good := f.file(camDir, "20261120-113000.mp4", 10*time.Minute, "good video")
	bad := f.file(camDir, "20261120-114000.mp4", 10*time.Minute, "corrupt")
	fresh := f.file(camDir, "20261120-115900.mp4", 30*time.Second, "corrupt") // may still be written
	orphan := f.file("99", "20261120-110000.mp4", 10*time.Minute, "good video")
	stranger := f.file(camDir, "notes.mp4", 10*time.Minute, "corrupt") // not jaga's naming
	indexed := f.segment(cam, time.Hour, 5, false)
	gone := f.segment(cam, 2*time.Hour, 5, false)
	if err := os.Remove(filepath.Join(f.j.DataDir, gone)); err != nil {
		t.Fatal(err)
	}

	res, err := f.j.Reconcile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res != (ReconcileResult{Indexed: 1, Removed: 2, RowsFixed: 1}) {
		t.Errorf("result = %+v", res)
	}

	start, end, size, ok := f.row(good)
	mtime := now.Add(-10 * time.Minute).UnixMilli()
	if !ok || end != mtime || end-start != 7000 || size != int64(len("good video")) {
		t.Errorf("good file row: ok=%v start=%d end=%d size=%d", ok, start, end, size)
	}
	if f.onDisk(bad) || f.onDisk(orphan) {
		t.Error("unreadable or orphaned recording kept")
	}
	if !f.onDisk(fresh) || !f.onDisk(stranger) {
		t.Error("a recent or foreign file was touched")
	}
	if _, _, _, ok := f.row(indexed); !ok || !f.onDisk(indexed) {
		t.Error("an indexed segment was changed")
	}
	if _, _, _, ok := f.row(gone); ok {
		t.Error("row of a missing file kept")
	}

	// A second pass has nothing left to do.
	if res, err := f.j.Reconcile(t.Context()); err != nil || res != (ReconcileResult{}) {
		t.Errorf("second pass = %+v, %v", res, err)
	}
	if !strings.Contains(f.logs.String(), "reason=unreadable") || !strings.Contains(f.logs.String(), `reason="camera deleted"`) {
		t.Errorf("deletions not logged:\n%s", f.logs)
	}
}

func TestReconcileNeedsProber(t *testing.T) {
	f := setup(t)
	cam := f.camera(nil)
	rel := f.file(itoa(cam), "20261120-110000.mp4", time.Hour, "good video")
	if _, err := f.j.Reconcile(t.Context()); err == nil {
		t.Fatal("ran without a prober")
	}
	if !f.onDisk(rel) {
		t.Fatal("deleted a recording although it could not be probed")
	}
}
