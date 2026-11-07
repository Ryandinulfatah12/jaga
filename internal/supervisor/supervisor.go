// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package supervisor runs one FFmpeg recorder per camera and indexes the
// segments it finishes into the segments table.
package supervisor

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/recorder"
)

// segmentName is the file name FFmpeg reports in the segment list, the base
// name of recorder.SegmentPattern.
const segmentName = "20060102-150405.mp4"

// Supervisor records cameras. Zero values are filled with defaults.
type Supervisor struct {
	FFmpegPath string
	DataDir    string
	DB         *sql.DB
	Log        *slog.Logger

	// Probe returns the duration of a finished segment. Defaults to ffprobe
	// next to FFmpegPath; on error the segment list's own times are used.
	Probe func(ctx context.Context, path string) (time.Duration, error)

	// RestartDelay is the wait before a recorder that exited is started again.
	// ponytail: fixed delay; F1.08 replaces it with exponential backoff and
	// stall detection.
	RestartDelay time.Duration
}

// Run records every camera until ctx is cancelled, then waits for all FFmpeg
// processes to close their last segment and exit.
func (s *Supervisor) Run(ctx context.Context, cams []camera.Camera) {
	var wg sync.WaitGroup
	for _, c := range cams {
		wg.Go(func() { s.runCamera(ctx, c) })
	}
	<-ctx.Done()
	wg.Wait()
}

func (s *Supervisor) runCamera(ctx context.Context, c camera.Camera) {
	log := s.log().With("camera", c.Name, "camera_id", c.ID)
	cmd, err := recorder.Build(s.FFmpegPath, s.DataDir, c)
	if err != nil {
		log.Error("cannot record camera", "err", err) // restarting cannot fix a bad config
		return
	}

	// FFmpeg never creates the date folder, so keep today's and tomorrow's
	// ready (docs/spikes/segment-index.md).
	s.ensureDirs(log, c.ID)
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.ensureDirs(log, c.ID)
			}
		}
	}()

	for {
		log.Info("recording")
		err := s.record(ctx, c, cmd, log)
		if ctx.Err() != nil {
			log.Info("recorder stopped")
			return
		}
		// Every exit the supervisor did not ask for is a failure, exit 0
		// included: FFmpeg exits 0 when the RTSP -timeout fires.
		delay := s.RestartDelay
		if delay <= 0 {
			delay = 2 * time.Second
		}
		log.Warn("recorder exited, restarting", "err", err, "in", delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		s.ensureDirs(log, c.ID) // e.g. after "Failed to open segment" when the clock jumped
	}
}

// record runs FFmpeg once and indexes the segments it reports until it exits.
func (s *Supervisor) record(ctx context.Context, c camera.Camera, rc recorder.Command, log *slog.Logger) error {
	cmd := exec.CommandContext(ctx, rc.Path, rc.Args...) //nolint:gosec // args come from recorder.Build, URL validated there
	cmd.Env = append(os.Environ(), rc.Env...)
	// SIGINT lets FFmpeg close and list the current segment; SIGKILL after 5 s.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	var last string // last stderr line: why FFmpeg stopped
	done := make(chan struct{})
	go func() {
		defer close(done)
		last = lastLine(stderr)
	}()

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		s.index(ctx, c.ID, sc.Text(), time.Now(), log)
	}
	<-done
	err = cmd.Wait()
	if last != "" {
		return fmt.Errorf("%w: %s", err, maskCredentials(last))
	}
	if err == nil {
		return errors.New("ffmpeg exited")
	}
	return err
}

// index inserts the segment named by one segment list line. The line arrives
// as FFmpeg closes the file, so that moment is the segment's end.
func (s *Supervisor) index(ctx context.Context, cameraID int64, line string, closedAt time.Time, log *slog.Logger) {
	fields := strings.Split(line, ",")
	name := fields[0]
	day, err := time.Parse(segmentName, name)
	if err != nil {
		log.Warn("ignoring unexpected segment list line", "line", line)
		return
	}
	rel := filepath.Join("recordings", strconv.FormatInt(cameraID, 10), day.Format(time.DateOnly), name)
	abs := filepath.Join(s.DataDir, rel)

	// The segment is already on disk and must be indexed even when
	// shutdown has cancelled ctx.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	info, err := os.Stat(abs)
	if err != nil {
		log.Warn("segment listed but missing", "path", rel, "err", err)
		return
	}
	dur, err := s.probe(ctx, abs)
	if err != nil {
		dur = csvDuration(fields)
		log.Debug("ffprobe failed, using segment list times", "path", rel, "err", err, "duration", dur)
	}
	end := closedAt.UnixMilli()
	start := end - dur.Milliseconds()

	if _, err := s.DB.ExecContext(ctx, `
		INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (path) DO NOTHING`,
		cameraID, rel, start, end, info.Size()); err != nil {
		log.Error("index segment", "path", rel, "err", err)
		return
	}
	log.Debug("segment", "path", rel, "duration", dur, "bytes", info.Size())
}

func (s *Supervisor) ensureDirs(log *slog.Logger, cameraID int64) {
	now := time.Now().UTC()
	for _, d := range []time.Time{now, now.Add(24 * time.Hour)} {
		dir := filepath.Join(recorder.Dir(s.DataDir, cameraID), d.Format(time.DateOnly))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			log.Error("create recordings folder", "dir", dir, "err", err)
		}
	}
}

func (s *Supervisor) probe(ctx context.Context, path string) (time.Duration, error) {
	if s.Probe != nil {
		return s.Probe(ctx, path)
	}
	return ffprobeDuration(ctx, ffprobePath(s.FFmpegPath), path)
}

func (s *Supervisor) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// ffprobePath assumes ffprobe sits next to ffmpeg, as in every FFmpeg build.
// ponytail: add an ffprobe_path setting if a setup ever splits them.
func ffprobePath(ffmpeg string) string {
	if !strings.ContainsRune(ffmpeg, filepath.Separator) {
		return "ffprobe"
	}
	return filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
}

func ffprobeDuration(ctx context.Context, ffprobe, path string) (time.Duration, error) {
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", //nolint:gosec // path is a segment under data_dir
		"-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, err
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec < 0 {
		return 0, fmt.Errorf("ffprobe duration %q", strings.TrimSpace(string(out)))
	}
	return time.Duration(sec * float64(time.Second)), nil
}

// csvDuration is end - start from a "name,start,end" segment list line. Less
// accurate than ffprobe for the first segment (docs/spikes/segment-index.md).
func csvDuration(fields []string) time.Duration {
	if len(fields) < 3 {
		return 0
	}
	start, err1 := strconv.ParseFloat(fields[1], 64)
	end, err2 := strconv.ParseFloat(fields[2], 64)
	if err1 != nil || err2 != nil || end < start {
		return 0
	}
	return time.Duration((end - start) * float64(time.Second))
}

func lastLine(r io.Reader) string {
	var last string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = l
		}
	}
	return last
}

// FFmpeg error messages can quote the input URL, password included.
var userinfo = regexp.MustCompile(`(://)[^/@\s]+@`)

func maskCredentials(s string) string {
	return userinfo.ReplaceAllString(s, "${1}***@")
}
