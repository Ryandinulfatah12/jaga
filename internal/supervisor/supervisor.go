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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/recorder"
	"github.com/ryandinulfatah12/jaga/internal/status"
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

	// Restart backoff after an exit or stall: BackoffMin, doubled per
	// consecutive failure, capped at BackoffMax (2 s and 60 s by default).
	// A recorder that ran for BackoffMax starts over at BackoffMin.
	BackoffMin, BackoffMax time.Duration

	// StallTimeout is how long FFmpeg may report no new frames before the
	// recorder is killed (30 s by default). Without a segment for twice
	// segment_sec plus StallTimeout, it is killed as well.
	StallTimeout time.Duration

	// StopTimeout is how long FFmpeg gets after SIGINT to close its segment
	// before SIGKILL (5 s by default). Keep the service manager's stop
	// timeout above it (Docker stop_grace_period, systemd TimeoutStopSec).
	StopTimeout time.Duration

	// Status receives every recorder state change. Optional.
	Status *status.Hub

	mu      sync.Mutex
	running map[int64]*recorderRun // by camera id
	wg      sync.WaitGroup
	ctx     context.Context
}

// recorderRun is one camera's recorder goroutine.
type recorderRun struct {
	cam  camera.Camera // as it was started; a different value means restart
	stop context.CancelFunc
	done chan struct{}
}

// offlineAfter is the number of consecutive failures that opens an offline event.
const offlineAfter = 3

// Run records the cameras that should record (enabled, record_mode
// continuous) until ctx is cancelled, then stops all recorders at once
// (SIGINT, SIGKILL after StopTimeout) and returns when every last segment is
// closed and indexed. Apply changes the set while it runs.
func (s *Supervisor) Run(ctx context.Context, cams []camera.Camera) {
	s.mu.Lock()
	s.ctx = ctx
	s.running = map[int64]*recorderRun{}
	s.mu.Unlock()
	s.Apply(cams)
	<-ctx.Done()
	began := time.Now()
	s.mu.Lock()
	n := len(s.running)
	s.mu.Unlock()
	s.log().Info("stopping recorders", "cameras", n)
	s.wg.Wait()
	s.log().Info("recorders stopped", "took", time.Since(began).Round(time.Millisecond))
}

// Apply makes the running recorders match cams, the full camera list:
// cameras that should record and are not get started, those that should not
// (deleted, disabled, record_mode off) get stopped, and those whose settings
// changed get restarted. Unchanged recorders keep running. It returns once
// stopped recorders have closed their last segment. Safe to call
// concurrently with itself; a no-op before Run or after shutdown.
func (s *Supervisor) Apply(cams []camera.Camera) {
	s.mu.Lock()
	if s.ctx == nil || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	want := map[int64]camera.Camera{}
	for _, c := range cams {
		if c.Enabled && c.RecordMode == camera.RecordContinuous {
			want[c.ID] = c
		}
	}
	var stopping []*recorderRun
	for id, r := range s.running {
		if c, ok := want[id]; !ok || !sameRecording(r.cam, c) {
			r.stop()
			stopping = append(stopping, r)
			delete(s.running, id)
		}
	}
	s.mu.Unlock()
	// Wait outside the lock: stopping takes up to StopTimeout, and a
	// restarted camera must not write a segment while the old FFmpeg still
	// closes its own.
	for _, r := range stopping {
		<-r.done
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	for id, c := range want {
		if _, ok := s.running[id]; ok {
			continue
		}
		ctx, stop := context.WithCancel(s.ctx)
		r := &recorderRun{cam: c, stop: stop, done: make(chan struct{})}
		s.running[id] = r
		s.wg.Go(func() {
			defer close(r.done)
			defer stop()
			s.runCamera(ctx, c)
			s.setStatus(status.Status{CameraID: c.ID, State: status.Stopped})
		})
	}
}

// sameRecording reports whether a and b record the same way, so a change
// to b (a rename, the retention days) needs no restart.
func sameRecording(a, b camera.Camera) bool {
	return a.MainURL == b.MainURL && a.RecordAudio == b.RecordAudio && a.SegmentSec == b.SegmentSec
}

func (s *Supervisor) setStatus(st status.Status) {
	if s.Status != nil {
		s.Status.Set(st)
	}
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

	// An offline event left open by a previous run ends now.
	s.closeOffline(ctx, log, c.ID, 0, time.Now())

	var (
		failures     int
		offlineID    int64
		offlineSince time.Time
	)
	onRunning := func() {
		s.setStatus(status.Status{CameraID: c.ID, State: status.Recording})
		log.Info("recording")
		if offlineID != 0 {
			s.closeOffline(ctx, log, c.ID, offlineID, time.Now())
			log.Info("camera online", "after", time.Since(offlineSince).Round(time.Second))
			offlineID = 0
		}
	}

	for {
		log.Debug("starting recorder")
		if offlineID == 0 {
			s.setStatus(status.Status{CameraID: c.ID, State: status.Starting, Failures: failures})
		}
		res := s.record(ctx, c, cmd, log, onRunning)
		if ctx.Err() != nil {
			log.Info("recorder stopped")
			return
		}
		// Every exit the supervisor did not ask for is a failure, exit 0
		// included: FFmpeg exits 0 when the RTSP -timeout fires.
		if res.running >= s.backoffMax() {
			failures = 0 // it recorded for a while: start the backoff over
		}
		failures++
		delay := s.backoff(failures)
		log.Warn("recorder failed, restarting", "err", res.err, "failures", failures, "in", delay)

		if failures == offlineAfter && offlineID == 0 {
			offlineSince = time.Now()
			offlineID = s.openOffline(ctx, log, c.ID, offlineSince, res.err)
		}
		st := status.Retrying
		if offlineID != 0 {
			st = status.Offline
		}
		s.setStatus(status.Status{CameraID: c.ID, State: st, Error: res.err.Error(), Failures: failures})
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		s.ensureDirs(log, c.ID) // e.g. after "Failed to open segment" when the clock jumped
	}
}

// backoff returns BackoffMin * 2^(failures-1), capped at BackoffMax.
func (s *Supervisor) backoff(failures int) time.Duration {
	d, limit := s.backoffMin(), s.backoffMax()
	for i := 1; i < failures && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

type result struct {
	err     error
	running time.Duration // how long FFmpeg reported progress; 0 if it never did
}

var errStalled = errors.New("stalled")

// record runs FFmpeg once and indexes the segments it reports until it
// exits, is stopped, or the watchdog kills it.
func (s *Supervisor) record(ctx context.Context, c camera.Camera, rc recorder.Command, log *slog.Logger, onRunning func()) result {
	runCtx, kill := context.WithCancelCause(ctx)
	defer kill(nil)

	cmd := exec.CommandContext(runCtx, rc.Path, rc.Args...) //nolint:gosec // args come from recorder.Build, URL validated there
	cmd.Env = append(os.Environ(), rc.Env...)
	// SIGINT lets FFmpeg close and list the current segment; SIGKILL after
	// StopTimeout if it does not exit.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = s.stopTimeout()

	progR, progW, err := os.Pipe()
	if err != nil {
		return result{err: err}
	}
	defer progR.Close()                //nolint:errcheck // read end, nothing to flush
	cmd.ExtraFiles = []*os.File{progW} // fd 3 = recorder.ProgressFD
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = progW.Close()
		return result{err: err}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = progW.Close()
		return result{err: err}
	}
	err = cmd.Start()
	_ = progW.Close() // the child holds its own copy
	if err != nil {
		return result{err: err}
	}

	var (
		mu          sync.Mutex
		lastFrame   = time.Now() // last progress with new frames, or the start
		lastSegment = time.Now()
		runningAt   time.Time
	)
	go func() {
		for range progressAdvances(progR) {
			mu.Lock()
			lastFrame = time.Now()
			first := runningAt.IsZero()
			if first {
				runningAt = lastFrame
			}
			mu.Unlock()
			if first {
				onRunning()
			}
		}
	}()

	var last string // last stderr line: why FFmpeg stopped
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		last = lastLine(stderr)
	}()

	// Watchdog: kill FFmpeg when frames or segments stop arriving.
	stall, segStall := s.stallTimeout(), 2*time.Duration(c.SegmentSec)*time.Second+s.stallTimeout()
	go func() {
		t := time.NewTicker(min(stall/4, time.Second))
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case now := <-t.C:
				mu.Lock()
				frameIdle, segIdle := now.Sub(lastFrame), now.Sub(lastSegment)
				mu.Unlock()
				switch {
				case frameIdle > stall:
					kill(fmt.Errorf("%w: no new frames for %s", errStalled, frameIdle.Round(time.Second)))
					return
				case segIdle > segStall:
					kill(fmt.Errorf("%w: no segment for %s", errStalled, segIdle.Round(time.Second)))
					return
				}
			}
		}
	}()

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		mu.Lock()
		lastSegment = time.Now()
		mu.Unlock()
		s.index(ctx, c.ID, sc.Text(), time.Now(), log)
	}
	<-stderrDone
	err = cmd.Wait()

	mu.Lock()
	var running time.Duration
	if !runningAt.IsZero() {
		running = time.Since(runningAt)
	}
	mu.Unlock()

	if cause := context.Cause(runCtx); errors.Is(cause, errStalled) {
		return result{err: cause, running: running}
	}
	switch {
	case err == nil && last != "": // exit 0, e.g. RTSP -timeout or end of stream
		err = errors.New(camera.MaskText(last))
	case err == nil:
		err = errors.New("ffmpeg exited")
	case last != "":
		err = fmt.Errorf("%w: %s", err, camera.MaskText(last))
	}
	return result{err: err, running: running}
}

// progressAdvances reads FFmpeg -progress blocks and yields once per block in
// which the frame count or output time moved forward.
func progressAdvances(r io.Reader) func(yield func(struct{}) bool) {
	return func(yield func(struct{}) bool) {
		var frame, outTime, prevFrame, prevOut int64 = 0, 0, -1, -1
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if !ok {
				continue
			}
			switch k {
			case "frame":
				frame, _ = strconv.ParseInt(v, 10, 64)
			case "out_time_us":
				outTime, _ = strconv.ParseInt(v, 10, 64) // "N/A" before the first packet parses as 0
			case "progress": // ends a block
				advanced := (frame > prevFrame && frame > 0) || (outTime > prevOut && outTime > 0)
				prevFrame, prevOut = max(prevFrame, frame), max(prevOut, outTime)
				if advanced && !yield(struct{}{}) {
					return
				}
			}
		}
	}
}

func (s *Supervisor) openOffline(ctx context.Context, log *slog.Logger, cameraID int64, at time.Time, reason error) int64 {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var id int64
	if err := s.DB.QueryRowContext(ctx, `
		INSERT INTO events (camera_id, type, start_ts, meta)
		VALUES (?, 'offline', ?, json_object('reason', ?))
		RETURNING id`, cameraID, at.UnixMilli(), fmt.Sprint(reason)).Scan(&id); err != nil {
		log.Error("record offline event", "err", err)
		return 0
	}
	log.Warn("camera offline", "failures", offlineAfter)
	return id
}

// closeOffline ends offline event id, or with id 0 every open offline event
// of the camera.
func (s *Supervisor) closeOffline(ctx context.Context, log *slog.Logger, cameraID, id int64, at time.Time) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := s.DB.ExecContext(ctx, `
		UPDATE events SET end_ts = ?
		WHERE camera_id = ? AND type = 'offline' AND end_ts IS NULL AND (? = 0 OR id = ?)`,
		at.UnixMilli(), cameraID, id, id); err != nil {
		log.Error("close offline event", "err", err)
	}
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
	return recorder.Duration(ctx, recorder.FFprobePath(s.FFmpegPath), path)
}

func (s *Supervisor) backoffMin() time.Duration { return orDefault(s.BackoffMin, 2*time.Second) }
func (s *Supervisor) backoffMax() time.Duration { return orDefault(s.BackoffMax, 60*time.Second) }
func (s *Supervisor) stallTimeout() time.Duration {
	return orDefault(s.StallTimeout, 30*time.Second)
}
func (s *Supervisor) stopTimeout() time.Duration { return orDefault(s.StopTimeout, 5*time.Second) }

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (s *Supervisor) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
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
