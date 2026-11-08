// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxSpan bounds one timeline query: a day per request keeps responses
// small (144 segments of 10 min per camera).
const maxSpan = 48 * time.Hour

type segmentOut struct {
	ID        int64 `json:"id"`
	Start     int64 `json:"start"` // Unix ms
	End       int64 `json:"end"`   // Unix ms
	Size      int64 `json:"size"`
	HasMotion bool  `json:"has_motion"`
}

// segments answers GET /cameras/{id}/segments?from=&to= (Unix ms): the
// recorded segments overlapping [from, to), oldest first.
func (s *Server) segments(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from, err1 := strconv.ParseInt(q.Get("from"), 10, 64)
	to, err2 := strconv.ParseInt(q.Get("to"), 10, 64)
	switch {
	case err1 != nil || err2 != nil || from < 0 || to <= from:
		writeError(w, http.StatusUnprocessableEntity, "from and to must be Unix milliseconds with from < to")
		return
	case time.Duration(to-from)*time.Millisecond > maxSpan:
		writeError(w, http.StatusUnprocessableEntity, "from..to may span at most 48 hours")
		return
	}
	if _, err := s.Cameras.Get(r.Context(), id); err != nil {
		s.cameraError(w, r, err)
		return
	}
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT id, start_ts, end_ts, size_bytes, has_motion FROM segments
		WHERE camera_id = ? AND start_ts < ? AND end_ts > ?
		ORDER BY start_ts`, id, to, from)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer rows.Close() //nolint:errcheck // rows.Err below reports iteration errors
	list := []segmentOut{}
	for rows.Next() {
		var seg segmentOut
		if err := rows.Scan(&seg.ID, &seg.Start, &seg.End, &seg.Size, &seg.HasMotion); err != nil {
			s.internal(w, r, err)
			return
		}
		list = append(list, seg)
	}
	if err := rows.Err(); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// segmentFile answers GET /segments/{id}/file: the MP4 with HTTP Range, so
// the browser can seek without downloading the whole segment. The path
// comes from the database and must stay inside data_dir/recordings.
func (s *Server) segmentFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var rel string
	err := s.DB.QueryRowContext(r.Context(), `SELECT path FROM segments WHERE id = ?`, id).Scan(&rel)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "segment not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	path, ok := recordingPath(s.DataDir, rel)
	if !ok {
		s.Log.Error("segment path outside recordings", "segment_id", id)
		writeError(w, http.StatusNotFound, "segment not found")
		return
	}
	f, err := os.Open(path) //nolint:gosec // G304: path from the DB, checked by recordingPath
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, "segment file is gone") // retention removed it
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer f.Close() //nolint:errcheck // read-only
	fi, err := f.Stat()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=3600") // finished segments never change
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="jaga-`+filepath.Base(path)+`"`)
	}
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// recordingPath resolves a segments.path and refuses anything that would
// land outside data_dir/recordings.
func recordingPath(dataDir, rel string) (string, bool) {
	inside, ok := strings.CutPrefix(filepath.ToSlash(rel), "recordings/")
	// IsLocal rejects absolute paths, "..", and empty or reserved names.
	if !ok || !filepath.IsLocal(inside) {
		return "", false
	}
	return filepath.Join(dataDir, "recordings", inside), true
}
