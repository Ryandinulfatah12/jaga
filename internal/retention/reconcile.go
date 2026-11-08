// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package retention

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// segmentName matches recorder.SegmentPattern's file part.
const segmentName = "20060102-150405.mp4"

// minAge keeps reconcile away from files FFmpeg may still be writing: the
// supervisor indexes a segment right after FFmpeg closes it, so an unindexed
// file this recent is most likely still open.
const minAge = 2 * time.Minute

// ReconcileResult summarizes one reconcile pass.
type ReconcileResult struct {
	Indexed   int // files on disk without a row, now indexed
	Removed   int // unreadable or orphaned files deleted
	RowsFixed int // rows whose file was gone, deleted
}

// Reconcile makes disk and database agree (docs/03-design.md):
//   - a recording without a row is indexed if ffprobe can read it
//     (end_ts = mtime, start_ts = end_ts - duration), deleted if it cannot;
//   - a recording of a camera that no longer exists is deleted;
//   - a row whose file is gone is deleted.
//
// It runs at startup and before every retention pass, alongside the
// supervisor: files younger than two minutes are left alone, so a segment
// FFmpeg is still writing is never touched.
func (j *Job) Reconcile(ctx context.Context) (ReconcileResult, error) {
	var res ReconcileResult
	if j.Probe == nil {
		// Without ffprobe every unindexed file would look unreadable.
		return res, errors.New("reconcile: no prober configured")
	}

	cams := map[int64]bool{}
	rows, err := j.DB.QueryContext(ctx, `SELECT id FROM cameras`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return res, err
		}
		cams[id] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return res, err
	}

	known, err := j.knownPaths(ctx)
	if err != nil {
		return res, err
	}

	// Files without a row.
	root := filepath.Join(j.DataDir, "recordings")
	files, _ := filepath.Glob(filepath.Join(root, "*", "????-??-??", "*.mp4"))
	cutoff := j.now().Add(-minAge)
	for _, abs := range files {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		rel, err := filepath.Rel(j.DataDir, abs)
		if err != nil || known[filepath.ToSlash(rel)] {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		cam, err := strconv.ParseInt(filepath.Base(filepath.Dir(filepath.Dir(abs))), 10, 64)
		if _, perr := time.Parse(segmentName, filepath.Base(abs)); err != nil || perr != nil {
			continue // not a file jaga wrote: leave it alone
		}
		if !cams[cam] {
			j.removeFile(abs, rel, "camera deleted")
			res.Removed++
			continue
		}
		dur, err := j.Probe(ctx, abs)
		if err != nil || dur <= 0 {
			j.removeFile(abs, rel, "unreadable")
			res.Removed++
			continue
		}
		end := info.ModTime().UnixMilli()
		if _, err := j.DB.ExecContext(ctx, `
			INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (path) DO NOTHING`,
			cam, filepath.ToSlash(rel), end-dur.Milliseconds(), end, info.Size()); err != nil {
			return res, err
		}
		res.Indexed++
	}

	// Rows without a file.
	for path := range known {
		abs := filepath.Join(j.DataDir, filepath.FromSlash(path))
		if _, err := os.Stat(abs); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if _, err := j.DB.ExecContext(ctx, `DELETE FROM segments WHERE path = ?`, path); err != nil {
			return res, err
		}
		res.RowsFixed++
	}

	if res != (ReconcileResult{}) {
		j.log().Info("reconcile", "indexed", res.Indexed, "removed_files", res.Removed, "removed_rows", res.RowsFixed)
	}
	return res, nil
}

func (j *Job) knownPaths(ctx context.Context) (map[string]bool, error) {
	rows, err := j.DB.QueryContext(ctx, `SELECT path FROM segments`)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return nil, err
		}
		known[filepath.ToSlash(p)] = true
	}
	return known, errors.Join(rows.Err(), rows.Close())
}

func (j *Job) removeFile(abs, rel, why string) {
	if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		j.log().Error("reconcile: delete file", "path", rel, "err", err)
		return
	}
	j.log().Warn("reconcile: deleted recording", "path", rel, "reason", why)
}
