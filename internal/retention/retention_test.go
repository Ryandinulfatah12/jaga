// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package retention

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/migrations"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t    *testing.T
	j    *Job
	disk int64 // bytes used by things other than recordings
	size int64 // pretend filesystem size
	logs *bytes.Buffer
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(t.Context(), filepath.Join(dir, "jaga.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(t.Context(), conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, size: 1000, logs: &bytes.Buffer{}}
	f.j = &Job{
		DB: conn, DataDir: dir, DefaultDays: 7, QuotaPercent: 90,
		Log: slog.New(slog.NewTextHandler(f.logs, nil)),
		Now: func() time.Time { return now },
		// Usage is the real size of the files under recordings plus f.disk.
		DiskUsage: func(string) (uint64, uint64, error) {
			used := f.disk + f.recorded()
			return uint64(used), uint64(f.size - used), nil //nolint:gosec // test sizes are small
		},
	}
	return f
}

func (f *fixture) recorded() int64 {
	var n int64
	_ = filepath.WalkDir(filepath.Join(f.j.DataDir, "recordings"), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func (f *fixture) camera(retentionDays any) int64 {
	f.t.Helper()
	var id int64
	if err := f.j.DB.QueryRowContext(f.t.Context(),
		`INSERT INTO cameras (name, main_url, retention_days) VALUES (?, x'00', ?) RETURNING id`,
		"cam"+strconv.FormatInt(time.Now().UnixNano(), 10), retentionDays).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// segment writes a file of size bytes that ended age ago and indexes it.
func (f *fixture) segment(cam int64, age time.Duration, size int, motion bool) string {
	f.t.Helper()
	end := now.Add(-age)
	rel := filepath.Join("recordings", strconv.FormatInt(cam, 10), end.Format(time.DateOnly), end.Format("20060102-150405")+".mp4")
	abs := filepath.Join(f.j.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(abs, bytes.Repeat([]byte("x"), size), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.j.DB.ExecContext(f.t.Context(),
		`INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes, has_motion) VALUES (?, ?, ?, ?, ?, ?)`,
		cam, rel, end.Add(-10*time.Minute).UnixMilli(), end.UnixMilli(), size, motion); err != nil {
		f.t.Fatal(err)
	}
	return rel
}

func (f *fixture) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.j.DataDir, rel))
	var rows int
	_ = f.j.DB.QueryRowContext(context.Background(), `SELECT count(*) FROM segments WHERE path = ?`, rel).Scan(&rows)
	if (err == nil) != (rows == 1) {
		f.t.Errorf("%s: file exists=%v but rows=%d", rel, err == nil, rows)
	}
	return err == nil
}

func (f *fixture) openDiskFull() int {
	var n int
	_ = f.j.DB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM events WHERE type = 'disk_full' AND end_ts IS NULL`).Scan(&n)
	return n
}

func TestAge(t *testing.T) {
	f := setup(t)
	def := f.camera(nil) // default 7 days
	short := f.camera(1)
	keep := f.segment(def, 6*24*time.Hour, 10, false)
	old := f.segment(def, 8*24*time.Hour, 10, true) // motion does not save it from age
	shortOld := f.segment(short, 30*time.Hour, 10, false)
	shortNew := f.segment(short, time.Hour, 10, false)

	res, err := f.j.Once(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 2 || res.Freed != 20 {
		t.Errorf("result = %+v", res)
	}
	if !f.exists(keep) || f.exists(old) || f.exists(shortOld) || !f.exists(shortNew) {
		t.Error("wrong segments deleted")
	}
	// The emptied date folder of the old segment is removed.
	if _, err := os.Stat(filepath.Dir(filepath.Join(f.j.DataDir, old))); err == nil {
		t.Error("empty old date folder kept")
	}
}

func TestQuotaDeletesOnlyTheExcessMotionLast(t *testing.T) {
	f := setup(t)
	cam := f.camera(nil)
	f.disk = 800                                       // 800 of 1000 used by other files
	motionOld := f.segment(cam, 5*time.Hour, 40, true) // oldest, but has motion
	plain1 := f.segment(cam, 4*time.Hour, 40, false)
	plain2 := f.segment(cam, 3*time.Hour, 40, false)
	plain3 := f.segment(cam, 2*time.Hour, 40, false)
	// Used 960 of 1000, quota 90% = 900: 60 bytes must go, two plain segments.

	res, err := f.j.Once(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 2 {
		t.Errorf("deleted %d, want 2 (only the excess)", res.Deleted)
	}
	if !f.exists(motionOld) || f.exists(plain1) || f.exists(plain2) || !f.exists(plain3) {
		t.Error("quota must delete the oldest segments without motion first")
	}
	if f.openDiskFull() != 0 {
		t.Error("disk_full opened although quota was reached")
	}
}

func TestDiskFullOpensAndCloses(t *testing.T) {
	f := setup(t)
	cam := f.camera(nil)
	f.disk = 950 // other files alone exceed the quota
	seg := f.segment(cam, time.Hour, 20, true)

	if _, err := f.j.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.exists(seg) {
		t.Error("segment kept while over quota")
	}
	if f.openDiskFull() != 1 {
		t.Fatal("disk_full not opened")
	}
	if _, err := f.j.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.openDiskFull() != 1 {
		t.Error("second run opened another disk_full")
	}
	f.disk = 100
	if _, err := f.j.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.openDiskFull() != 0 || !strings.Contains(f.logs.String(), "disk back under quota") {
		t.Errorf("disk_full not closed:\n%s", f.logs)
	}
}

func TestRefusesPathsOutsideRecordings(t *testing.T) {
	f := setup(t)
	cam := f.camera(nil)
	victim := filepath.Join(f.j.DataDir, "jaga.keep")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"jaga.keep", "recordings/../jaga.keep", "recordings/1/../../jaga.keep", "/etc/hostname", "recordings", "recordings/"} {
		if _, err := f.j.DB.ExecContext(t.Context(),
			`INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes) VALUES (?, ?, 0, 1, 1)`, cam, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.j.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("retention deleted a file outside recordings/")
	}
	if !strings.Contains(f.logs.String(), "outside data_dir/recordings") {
		t.Errorf("not reported:\n%s", f.logs)
	}
}

func TestMissingFileStillRemovesRow(t *testing.T) {
	f := setup(t)
	cam := f.camera(nil)
	rel := f.segment(cam, 10*24*time.Hour, 10, false)
	if err := os.Remove(filepath.Join(f.j.DataDir, rel)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.j.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.j.DB.QueryRowContext(t.Context(), `SELECT count(*) FROM segments`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("row of an already deleted file kept")
	}
}

func TestKeepsTodayAndTomorrowFolders(t *testing.T) {
	f := setup(t)
	for _, d := range []string{"2026-11-20", "2026-11-21", "2026-11-19"} {
		if err := os.MkdirAll(filepath.Join(f.j.DataDir, "recordings", "1", d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.j.Once(t.Context()); err != nil {
		t.Fatal(err)
	}
	for d, want := range map[string]bool{"2026-11-20": true, "2026-11-21": true, "2026-11-19": false} {
		_, err := os.Stat(filepath.Join(f.j.DataDir, "recordings", "1", d))
		if (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", d, err == nil, want)
		}
	}
}

func TestStatfs(t *testing.T) {
	used, avail, err := statfs(t.TempDir())
	if err != nil || used == 0 || avail == 0 {
		t.Errorf("statfs = %d, %d, %v", used, avail, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
