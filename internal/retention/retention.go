// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package retention deletes recordings by age and keeps the disk under quota.
package retention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Interval is how often the job runs (docs/03-design.md).
const Interval = 10 * time.Minute

// batch is how many segments one query deletes.
const batch = 500

// Job deletes segments older than their camera's retention_days and, when
// disk usage of data_dir passes QuotaPercent, the oldest segments overall:
// those without motion first.
type Job struct {
	DB           *sql.DB
	DataDir      string
	DefaultDays  int // storage.default_retention_days, for cameras with NULL retention_days
	QuotaPercent int // storage.disk_quota_percent
	Log          *slog.Logger

	// DiskUsage reports used and available bytes of the filesystem holding
	// path, like df. Defaults to statfs. Replaced in tests.
	DiskUsage func(path string) (used, avail uint64, err error)
	// Now defaults to time.Now. Replaced in tests.
	Now func() time.Time
	// Probe returns a recording's duration, for Reconcile. Required.
	Probe func(ctx context.Context, path string) (time.Duration, error)
}

// Result summarizes one run.
type Result struct {
	Deleted int
	Freed   int64 // bytes
}

// Run reconciles disk and database, then applies retention, at start and
// every Interval until ctx is cancelled.
func (j *Job) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		if _, err := j.Reconcile(ctx); err != nil && ctx.Err() == nil {
			j.log().Error("reconcile", "err", err)
		}
		res, err := j.Once(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			j.log().Error("retention", "err", err)
		case res.Deleted > 0:
			j.log().Info("retention", "deleted", res.Deleted, "freed_bytes", res.Freed)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once runs the job a single time.
func (j *Job) Once(ctx context.Context) (Result, error) {
	var total Result
	now := j.now()

	// 1. Age: end_ts older than the camera's retention.
	for {
		res, n, err := j.deleteBatch(ctx, `
			SELECT s.id, s.path, s.size_bytes FROM segments s JOIN cameras c ON c.id = s.camera_id
			WHERE s.end_ts < ? - coalesce(c.retention_days, ?) * 86400000
			ORDER BY s.start_ts LIMIT ?`, now.UnixMilli(), j.DefaultDays, batch)
		total.Deleted += res.Deleted
		total.Freed += res.Freed
		if err != nil {
			return total, err
		}
		if n < batch || res.Deleted == 0 {
			break // done, or every file in the batch failed: retry next run
		}
	}

	// 2. Quota: free the excess, oldest first, segments without motion first.
	need, err := j.excess()
	if err != nil {
		return total, err
	}
	for need > 0 {
		// Only as many segments as it takes to free need bytes, so one run
		// never deletes far more than the excess.
		res, n, err := j.deleteBatch(ctx, `
			SELECT id, path, size_bytes FROM (
				SELECT id, path, size_bytes, has_motion, start_ts,
					sum(size_bytes) OVER (ORDER BY has_motion, start_ts, id ROWS UNBOUNDED PRECEDING) AS upto
				FROM segments)
			WHERE upto - size_bytes < ?
			ORDER BY has_motion, start_ts, id LIMIT ?`, need, batch)
		total.Deleted += res.Deleted
		total.Freed += res.Freed
		if err != nil {
			return total, err
		}
		need -= res.Freed
		if n == 0 || res.Deleted == 0 {
			break // nothing (deletable) left
		}
	}

	// 3. Still over quota means the disk is full of something else.
	if need, err = j.excess(); err != nil {
		return total, err
	}
	if err := j.diskFull(ctx, need > 0, now); err != nil {
		return total, err
	}

	j.removeEmptyDays(now)
	return total, nil
}

// deleteBatch deletes the files and rows returned by query (id, path, size).
// n is the number of rows the query returned.
func (j *Job) deleteBatch(ctx context.Context, query string, args ...any) (res Result, n int, err error) {
	rows, err := j.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return res, 0, err
	}
	type seg struct {
		id   int64
		path string
		size int64
	}
	var segs []seg
	for rows.Next() {
		var s seg
		if err := rows.Scan(&s.id, &s.path, &s.size); err != nil {
			_ = rows.Close()
			return res, 0, err
		}
		segs = append(segs, s)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return res, 0, err
	}

	var ids []int64
	for _, s := range segs {
		abs, ok := j.recordingPath(s.path)
		if !ok {
			j.log().Error("segment path outside data_dir/recordings, not deleting", "id", s.id, "path", s.path)
			continue
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			j.log().Error("delete segment file", "path", s.path, "err", err)
			continue // keep the row so a later run retries
		}
		ids = append(ids, s.id)
		res.Freed += s.size
	}
	if err := j.deleteRows(ctx, ids); err != nil {
		return res, len(segs), err
	}
	res.Deleted = len(ids)
	return res, len(segs), nil
}

// deleteRows removes segment rows in one transaction.
func (j *Job) deleteRows(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := j.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM segments WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// recordingPath resolves a segments.path and refuses anything that would
// land outside data_dir/recordings.
func (j *Job) recordingPath(rel string) (string, bool) {
	inside, ok := strings.CutPrefix(filepath.ToSlash(rel), "recordings/")
	// IsLocal rejects absolute paths, "..", and empty or reserved names.
	if !ok || !filepath.IsLocal(inside) {
		return "", false
	}
	return filepath.Join(j.DataDir, "recordings", inside), true
}

// excess returns how many bytes must go to bring usage to QuotaPercent.
func (j *Job) excess() (int64, error) {
	used, avail, err := j.usage(j.DataDir)
	if err != nil {
		return 0, fmt.Errorf("disk usage of %s: %w", j.DataDir, err)
	}
	limit := (used + avail) / 100 * uint64(j.QuotaPercent) //nolint:gosec // QuotaPercent is validated to 1-99
	if used <= limit {
		return 0, nil
	}
	return int64(used - limit), nil //nolint:gosec // bounded by the disk size
}

// diskFull opens a disk_full event while the disk stays over quota and
// closes it once it is back under.
func (j *Job) diskFull(ctx context.Context, full bool, now time.Time) error {
	var open sql.NullInt64
	err := j.DB.QueryRowContext(ctx,
		`SELECT id FROM events WHERE type = 'disk_full' AND end_ts IS NULL LIMIT 1`).Scan(&open)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = nil
	switch {
	case full && !open.Valid:
		j.log().Error("disk still over quota after deleting recordings", "data_dir", j.DataDir, "quota_percent", j.QuotaPercent)
		_, err = j.DB.ExecContext(ctx, `INSERT INTO events (type, start_ts) VALUES ('disk_full', ?)`, now.UnixMilli())
	case !full && open.Valid:
		j.log().Info("disk back under quota")
		_, err = j.DB.ExecContext(ctx, `UPDATE events SET end_ts = ? WHERE id = ?`, now.UnixMilli(), open.Int64)
	}
	return err
}

// removeEmptyDays deletes empty date folders older than today. Today's and
// tomorrow's are kept: the supervisor creates them ahead of FFmpeg.
func (j *Job) removeEmptyDays(now time.Time) {
	today := now.UTC().Format(time.DateOnly)
	days, _ := filepath.Glob(filepath.Join(j.DataDir, "recordings", "*", "????-??-??"))
	for _, d := range days {
		if name := filepath.Base(d); name < today {
			_ = os.Remove(d) // fails, as intended, while the folder is not empty
		}
	}
}

func (j *Job) usage(path string) (used, avail uint64, err error) {
	if j.DiskUsage != nil {
		return j.DiskUsage(path)
	}
	return statfs(path)
}

// statfs reports usage the way df does: used = total - free, avail = space
// available to unprivileged users.
func statfs(path string) (used, avail uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bs := uint64(st.Bsize) //nolint:gosec,unconvert // int64 on Linux, uint32 on macOS
	return (st.Blocks - st.Bfree) * bs, st.Bavail * bs, nil
}

func (j *Job) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

func (j *Job) log() *slog.Logger {
	if j.Log != nil {
		return j.Log
	}
	return slog.Default()
}
