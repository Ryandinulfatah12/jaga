// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxClip bounds one export: two hours of a 2K stream is a few GB.
const maxClip = 2 * time.Hour

// clipSlots caps concurrent exports, one FFmpeg each, so a signed-in user
// cannot fill the CPU and disk bandwidth the recorders need.
var clipSlots = make(chan struct{}, 2)

// clip answers GET /cameras/{id}/clip?from=&to= (Unix ms): the recorded
// segments in [from, to) joined into one MP4 by FFmpeg's concat demuxer
// with -c copy, streamed straight to the browser (no temp file). Cuts land
// on keyframes, so the clip may start up to one GOP before `from`. Gaps
// in the recording are simply skipped.
func (s *Server) clip(w http.ResponseWriter, r *http.Request) {
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
	case time.Duration(to-from)*time.Millisecond > maxClip:
		writeError(w, http.StatusUnprocessableEntity, "a clip may be at most 2 hours")
		return
	case s.FFmpeg == "":
		writeError(w, http.StatusServiceUnavailable, "ffmpeg is not available")
		return
	}
	cam, err := s.Cameras.Get(r.Context(), id)
	if err != nil {
		s.cameraError(w, r, err)
		return
	}
	select {
	case clipSlots <- struct{}{}:
		defer func() { <-clipSlots }()
	default:
		writeError(w, http.StatusTooManyRequests, "two exports are already running, try again when one finishes")
		return
	}
	list, first, err := s.concatList(r.Context(), id, from, to)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if list == "" {
		writeError(w, http.StatusNotFound, "no recordings in this range")
		return
	}

	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		// The list is ours: built from the DB, every path checked by
		// recordingPath. -safe 0 only because the paths are absolute.
		"-f", "concat", "-safe", "0", "-protocol_whitelist", "file,pipe",
		"-i", "pipe:0",
		"-t", msToSec(to - max(from, first)),
		"-map", "0", "-c", "copy",
		// Fragmented MP4 can be written to a pipe and plays while downloading.
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1",
	}
	ctx, cancel := context.WithCancel(r.Context()) // client gone: FFmpeg is killed
	defer cancel()
	cmd := exec.CommandContext(ctx, s.FFmpeg, args...) //nolint:gosec // fixed args; input list built from the DB
	cmd.Stdin = strings.NewReader(list)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := cmd.Start(); err != nil {
		s.internal(w, r, err)
		return
	}
	// Wait for the first bytes so an FFmpeg failure is still a 500, not
	// an empty 200 download.
	br := bufio.NewReaderSize(out, 64<<10)
	if _, err := br.Peek(1); err != nil {
		_ = cmd.Wait()
		s.Log.Error("clip export failed", "camera_id", id, "ffmpeg", strings.TrimSpace(stderr.String()))
		writeError(w, http.StatusInternalServerError, "export failed")
		return
	}
	name := fmt.Sprintf("jaga-%s-%s.mp4", safeName(cam.Name), time.UnixMilli(from).Format("20060102-150405"))
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	n, copyErr := io.Copy(w, br)
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		// Headers are gone already; the browser sees a short download.
		s.Log.Warn("clip export failed", "camera_id", id, "bytes", n, "err", err, "ffmpeg", strings.TrimSpace(stderr.String()))
	} else if copyErr == nil {
		s.Log.Info("clip exported", "camera_id", id, "from", from, "to", to, "bytes", n, "by", currentUser(r).Username)
	}
}

// concatList builds an FFmpeg concat list of the segments overlapping
// [from, to) whose files still exist, and returns the start of the first.
// The first entry gets an inpoint at `from`: input -ss does not seek a
// concat list read from a pipe.
func (s *Server) concatList(ctx context.Context, id, from, to int64) (string, int64, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT path, start_ts FROM segments
		WHERE camera_id = ? AND start_ts < ? AND end_ts > ?
		ORDER BY start_ts`, id, to, from)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close() //nolint:errcheck // rows.Err below reports iteration errors
	var b strings.Builder
	var first int64 = -1
	b.WriteString("ffconcat version 1.0\n")
	for rows.Next() {
		var rel string
		var start int64
		if err := rows.Scan(&rel, &start); err != nil {
			return "", 0, err
		}
		path, ok := recordingPath(s.DataDir, rel)
		// A line break would start a new concat directive (file, inpoint...).
		if !ok || strings.ContainsAny(path, "\r\n") {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue // removed by retention since the query
		}
		if first < 0 {
			first = start
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", 0, err
		}
		// file: URL, because a list read from pipe: resolves bare paths
		// against "pipe:". Single quotes are the concat format's quoting;
		// recordingPath never yields one, but escape anyway.
		b.WriteString("file 'file:" + strings.ReplaceAll(abs, "'", `'\''`) + "'\n")
		if start == first && from > start {
			b.WriteString("inpoint " + msToSec(from-start) + "\n")
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	if first < 0 {
		return "", 0, nil
	}
	return b.String(), first, nil
}

func msToSec(ms int64) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }

// safeName keeps letters, digits, '-' and '_' for a download file name.
func safeName(s string) string {
	b := []rune{}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b = append(b, r)
		case r == ' ':
			b = append(b, '-')
		}
	}
	if len(b) == 0 {
		return "camera"
	}
	return filepath.Base(string(b))
}
