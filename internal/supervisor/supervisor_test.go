// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/internal/status"
	"github.com/ryandinulfatah12/jaga/migrations"
)

// The test binary doubles as a fake ffmpeg when JAGA_FAKE_FFMPEG is set.
func TestMain(m *testing.M) {
	switch os.Getenv("JAGA_FAKE_FFMPEG") {
	case "":
		os.Exit(m.Run())
	case "record":
		fakeRecord()
	case "fail":
		countRun()
		fakeFail()
	case "stall":
		countRun()
		fakeStall()
	case "flaky": // fails twice, then records
		if countRun() <= 2 {
			fakeFail()
		}
		fakeRecord()
	case "exit0": // RTSP -timeout: FFmpeg exits 0 after an error line
		countRun()
		fmt.Fprintln(os.Stderr, "[in#0/rtsp @ 0x1] Failed reading RTSP data: End of file")
		os.Exit(0)
	case "deaf": // ignores SIGINT, like a hung FFmpeg
		signal.Ignore(os.Interrupt)
		progress(1)
		time.Sleep(time.Minute)
	case "flaky3": // fails offlineAfter times, then records
		if countRun() <= offlineAfter {
			fakeFail()
		}
		fakeRecord()
	}
}

// progress writes one FFmpeg -progress block to fd 3.
func progress(frame int) {
	f := os.NewFile(3, "progress")
	_, _ = fmt.Fprintf(f, "frame=%d\nout_time_us=%d\nprogress=continue\n", frame, frame*40000)
}

// countRun appends to $JAGA_FAKE_COUNT and returns how many runs it holds.
func countRun() int {
	path := os.Getenv("JAGA_FAKE_COUNT")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // test fake, temp file set by the test
	if err == nil {
		_, _ = f.WriteString("run\n")
		_ = f.Close()
	}
	b, _ := os.ReadFile(path) //nolint:gosec // test fake, temp file set by the test
	return strings.Count(string(b), "run")
}

// fakeRecord reports progress, writes two segments, waits for SIGINT, then
// closes a third one like FFmpeg does on a clean stop.
func fakeRecord() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	out := os.Args[len(os.Args)-1] // .../recordings/<id>/%Y-%m-%d/%Y%m%d-%H%M%S.mp4
	camDir := filepath.Dir(filepath.Dir(out))
	segment := func(i int) {
		t := time.Now().UTC().Add(time.Duration(i) * time.Second)
		name := t.Format("20060102-150405") + ".mp4"
		if err := os.WriteFile(filepath.Join(camDir, t.Format(time.DateOnly), name), []byte("fake mp4 data"), 0o600); err != nil { //nolint:gosec // test fake, path from the supervisor's own argv
			fmt.Fprintln(os.Stderr, "Failed to open segment:", err)
			os.Exit(254)
		}
		fmt.Printf("%s,%d.000000,%d.000000\n", name, i*4, i*4+4)
	}
	frame := 1
	progress(frame)
	segment(0)
	segment(1)
	t := time.NewTicker(50 * time.Millisecond)
	for {
		select {
		case <-t.C:
			frame += 2
			progress(frame)
		case <-stop:
			segment(2)
			os.Exit(255) // FFmpeg's exit code after SIGINT
		}
	}
}

// fakeFail exits like a refused camera connection.
func fakeFail() {
	fmt.Fprintln(os.Stderr, "[tcp @ 0x1] Connection to tcp://cam:554 failed: Connection refused")
	fmt.Fprintln(os.Stderr, "rtsp://admin:hunter2@cam:554/stream1: Connection refused")
	os.Exit(1)
}

// fakeStall connects, then keeps reporting the same frame count, like a
// camera that froze while the connection stays open.
func fakeStall() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	t := time.NewTicker(50 * time.Millisecond)
	for {
		select {
		case <-t.C:
			progress(10)
		case <-stop:
			os.Exit(255)
		}
	}
}

type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func setup(t *testing.T, mode string) (*Supervisor, camera.Camera, *logBuf) {
	t.Helper()
	t.Setenv("JAGA_FAKE_FFMPEG", mode)
	ctx := t.Context()
	dir := t.TempDir()
	conn, err := db.Open(ctx, filepath.Join(dir, "jaga.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	seal, err := camera.NewSealer("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	c, err := camera.NewStore(conn, seal).Create(ctx, camera.New("Depan", "rtsp://admin:hunter2@cam:554/stream1"))
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logs := &logBuf{}
	s := &Supervisor{
		FFmpegPath:   self,
		DataDir:      dir,
		DB:           conn,
		Log:          slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Probe:        func(context.Context, string) (time.Duration, error) { return 4 * time.Second, nil },
		BackoffMin:   20 * time.Millisecond,
		BackoffMax:   80 * time.Millisecond,
		StallTimeout: 2 * time.Second,
	}
	t.Setenv("JAGA_FAKE_COUNT", filepath.Join(t.TempDir(), "runs"))
	return s, c, logs
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func count(t *testing.T, s *Supervisor) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(context.Background(), `SELECT count(*) FROM segments`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIndexesSegmentsAndStopsCleanly(t *testing.T) {
	s, c, logs := setup(t, "record")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx, []camera.Camera{c}); close(done) }()

	waitFor(t, "two segments", func() bool { return count(t, s) == 2 })
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	// The segment closed during shutdown is indexed too.
	if n := count(t, s); n != 3 {
		t.Fatalf("segments = %d, want 3\n%s", n, logs)
	}

	rows, err := s.DB.QueryContext(context.Background(), `SELECT path, start_ts, end_ts, size_bytes FROM segments ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var path string
		var start, end, size int64
		if err := rows.Scan(&path, &start, &end, &size); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(path, "recordings/1/") || end-start != 4000 || size != int64(len("fake mp4 data")) {
			t.Errorf("segment %s start=%d end=%d size=%d", path, start, end, size)
		}
		if _, err := os.Stat(filepath.Join(s.DataDir, path)); err != nil {
			t.Errorf("indexed path does not exist: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "restarting") {
		t.Errorf("a requested stop was treated as a failure:\n%s", logs)
	}

	// Tomorrow's folder exists before midnight.
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format(time.DateOnly)
	if _, err := os.Stat(filepath.Join(s.DataDir, "recordings", "1", tomorrow)); err != nil {
		t.Errorf("tomorrow's folder: %v", err)
	}
}

func runs(t *testing.T) int {
	t.Helper()
	b, _ := os.ReadFile(os.Getenv("JAGA_FAKE_COUNT")) //nolint:gosec // temp file set by the test
	return strings.Count(string(b), "run")
}

func events(t *testing.T, s *Supervisor) (open, closed int) {
	t.Helper()
	if err := s.DB.QueryRowContext(context.Background(), `
		SELECT count(*) FILTER (WHERE end_ts IS NULL), count(*) FILTER (WHERE end_ts IS NOT NULL)
		FROM events WHERE type = 'offline'`).Scan(&open, &closed); err != nil {
		t.Fatal(err)
	}
	return open, closed
}

func start(t *testing.T, s *Supervisor, c camera.Camera) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx, []camera.Camera{c}); close(done) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("Run did not return after cancel")
		}
	}
}

func TestRestartsWithBackoffAndOffline(t *testing.T) {
	s, c, logs := setup(t, "fail")
	stop := start(t, s, c)
	waitFor(t, "five runs", func() bool { return runs(t) >= 5 })
	stop()

	l := logs.String()
	if !strings.Contains(l, "recorder failed, restarting") || !strings.Contains(l, "Connection refused") {
		t.Errorf("restart reason not logged:\n%s", l)
	}
	if strings.Contains(l, "hunter2") {
		t.Errorf("camera password leaked into the log:\n%s", l)
	}
	for _, want := range []string{"in=20ms", "in=40ms", "in=80ms"} {
		if !strings.Contains(l, want) {
			t.Errorf("backoff step %s missing:\n%s", want, l)
		}
	}
	if strings.Contains(l, "in=160ms") {
		t.Errorf("backoff exceeded BackoffMax:\n%s", l)
	}
	if open, _ := events(t, s); open != 1 {
		t.Errorf("open offline events = %d, want 1", open)
	}
}

func TestExitZeroIsAFailure(t *testing.T) {
	s, c, logs := setup(t, "exit0")
	stop := start(t, s, c)
	waitFor(t, "a restart after exit 0", func() bool { return runs(t) >= 2 })
	stop()
	l := logs.String()
	if !strings.Contains(l, `err="[in#0/rtsp @ 0x1] Failed reading RTSP data: End of file"`) || strings.Contains(l, "%!") {
		t.Errorf("exit 0 reason not logged cleanly:\n%s", l)
	}
}

func TestStallIsKilled(t *testing.T) {
	s, c, logs := setup(t, "stall")
	s.StallTimeout = 300 * time.Millisecond
	stop := start(t, s, c)
	waitFor(t, "a restart after the stall", func() bool { return runs(t) >= 2 })
	stop()
	if !strings.Contains(logs.String(), "stalled: no new frames") {
		t.Errorf("stall not reported:\n%s", logs)
	}
}

func TestOnlineClosesOffline(t *testing.T) {
	s, c, logs := setup(t, "flaky")
	// A stale open event from an earlier run is closed at start.
	if _, err := s.DB.ExecContext(t.Context(), `INSERT INTO events (camera_id, type, start_ts) VALUES (?, 'offline', 0)`, c.ID); err != nil {
		t.Fatal(err)
	}
	stop := start(t, s, c)
	waitFor(t, "recording after two failures", func() bool { return count(t, s) >= 2 })
	stop()
	// flaky fails twice, below offlineAfter, so only the stale event exists.
	if open, closed := events(t, s); open != 0 || closed != 1 {
		t.Errorf("offline events open=%d closed=%d, want 0 and 1\n%s", open, closed, logs)
	}
}

func TestOfflineThenOnline(t *testing.T) {
	s, c, logs := setup(t, "flaky3")
	stop := start(t, s, c)
	waitFor(t, "recording after three failures", func() bool { return count(t, s) >= 2 })
	stop()
	if open, closed := events(t, s); open != 0 || closed != 1 {
		t.Errorf("offline events open=%d closed=%d, want 0 and 1\n%s", open, closed, logs)
	}
	if !strings.Contains(logs.String(), "camera offline") || !strings.Contains(logs.String(), "camera online") {
		t.Errorf("offline/online not logged:\n%s", logs)
	}
}

func TestShutdownStopsAllCameras(t *testing.T) {
	s, first, logs := setup(t, "record")
	cams := []camera.Camera{first}
	store := camera.NewStore(s.DB, mustSealer(t))
	for _, name := range []string{"Garasi", "Belakang"} {
		c, err := store.Create(t.Context(), camera.New(name, "rtsp://cam/"+name))
		if err != nil {
			t.Fatal(err)
		}
		cams = append(cams, c)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx, cams); close(done) }()
	waitFor(t, "two segments per camera", func() bool { return count(t, s) == 2*len(cams) })

	began := time.Now()
	cancel()
	<-done
	// Recorders stop in parallel, well within one StopTimeout.
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("shutdown took %v", took)
	}
	var perCamera int
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT min(n) FROM (SELECT count(*) AS n FROM segments GROUP BY camera_id)`).Scan(&perCamera); err != nil {
		t.Fatal(err)
	}
	if perCamera != 3 || count(t, s) != 3*len(cams) {
		t.Errorf("want 3 segments per camera (last one closed on SIGINT), got min %d total %d\n%s", perCamera, count(t, s), logs)
	}
	if !strings.Contains(logs.String(), "recorders stopped") {
		t.Errorf("no shutdown summary:\n%s", logs)
	}
}

func TestShutdownKillsHungRecorder(t *testing.T) {
	s, c, logs := setup(t, "deaf")
	s.StopTimeout = 300 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx, []camera.Camera{c}); close(done) }()
	waitFor(t, "recording", func() bool { return strings.Contains(logs.String(), "msg=recording") })

	began := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a recorder ignoring SIGINT blocked shutdown")
	}
	if took := time.Since(began); took < 300*time.Millisecond || took > 5*time.Second {
		t.Errorf("shutdown took %v, want about StopTimeout", took)
	}
}

func TestShutdownDuringBackoff(t *testing.T) {
	s, c, _ := setup(t, "fail")
	s.BackoffMin, s.BackoffMax = time.Minute, time.Minute
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx, []camera.Camera{c}); close(done) }()
	waitFor(t, "first failure", func() bool { return runs(t) >= 1 })
	time.Sleep(50 * time.Millisecond)
	began := time.Now()
	cancel()
	<-done
	if took := time.Since(began); took > time.Second {
		t.Errorf("stop during a 1 min backoff took %v", took)
	}
}

func mustSealer(t *testing.T) *camera.Sealer {
	t.Helper()
	s, err := camera.NewSealer("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBackoff(t *testing.T) {
	s := &Supervisor{}
	want := []time.Duration{2, 4, 8, 16, 32, 60, 60}
	for i, w := range want {
		if got := s.backoff(i + 1); got != w*time.Second {
			t.Errorf("backoff(%d) = %v, want %v", i+1, got, w*time.Second)
		}
	}
}

func TestProgressAdvances(t *testing.T) {
	in := strings.Join([]string{
		"frame=0", "out_time_us=N/A", "progress=continue", // connecting: no advance
		"frame=25", "out_time_us=1000000", "progress=continue", // advance
		"frame=25", "out_time_us=1000000", "progress=continue", // frozen
		"frame=25", "out_time_us=2000000", "progress=continue", // time moved: advance
		"frame=50", "out_time_us=3000000", "progress=end", // advance
	}, "\n")
	n := 0
	for range progressAdvances(strings.NewReader(in)) {
		n++
	}
	if n != 3 {
		t.Errorf("advances = %d, want 3", n)
	}
}

func TestIndexIgnoresBadLines(t *testing.T) {
	s, c, logs := setup(t, "")
	s.ensureDirs(s.log(), c.ID)
	for _, line := range []string{"", "nonsense", "../../etc/passwd,0,1", "20261107-044513.mp4,0,4"} {
		s.index(t.Context(), c.ID, line, time.Now(), s.log())
	}
	if n := count(t, s); n != 0 {
		t.Errorf("segments = %d, want 0 (listed file does not exist)", n)
	}
	if !strings.Contains(logs.String(), "ignoring unexpected segment list line") || !strings.Contains(logs.String(), "segment listed but missing") {
		t.Errorf("log:\n%s", logs)
	}
}

func TestHelpers(t *testing.T) {
	if d := csvDuration(strings.Split("a.mp4,1.5,5.5", ",")); d != 4*time.Second {
		t.Errorf("csvDuration = %v", d)
	}
	if d := csvDuration([]string{"a.mp4", "x"}); d != 0 {
		t.Errorf("csvDuration short = %v", d)
	}
}

// segmentsOf counts the indexed segments of one camera.
func segmentsOf(t *testing.T, s *Supervisor, id int64) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(context.Background(), `SELECT count(*) FROM segments WHERE camera_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApplyStartsStopsAndRestarts(t *testing.T) {
	s, first, logs := setup(t, "record")
	hub := status.NewHub()
	s.Status = hub
	updates, cancelSub := hub.Subscribe()
	defer cancelSub()
	store := camera.NewStore(s.DB, mustSealer(t))
	second, err := store.Create(t.Context(), camera.New("Garasi", "rtsp://cam/garasi"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx, []camera.Camera{first}); close(done) }()
	waitFor(t, "first camera recording", func() bool { st, _ := hub.Get(first.ID); return st.State == status.Recording })
	if _, ok := hub.Get(second.ID); ok {
		t.Fatal("second camera has a status before Apply")
	}

	// Add the second camera; the first keeps its recorder (one run only).
	s.Apply([]camera.Camera{first, second})
	waitFor(t, "second camera recording", func() bool { st, _ := hub.Get(second.ID); return st.State == status.Recording })
	waitFor(t, "second camera segments", func() bool { return segmentsOf(t, s, second.ID) >= 2 })

	// A rename does not restart; a new URL does, after the old FFmpeg closed
	// its segment (2 at start + 1 on SIGINT).
	renamed := first
	renamed.Name = "Depan baru"
	s.Apply([]camera.Camera{renamed, second})
	if st, _ := hub.Get(first.ID); st.State != status.Recording {
		t.Errorf("rename restarted the recorder: %+v", st)
	}
	moved := renamed
	moved.MainURL = "rtsp://cam/depan2"
	restarted := time.Now()
	s.Apply([]camera.Camera{moved, second})
	if n := segmentsOf(t, s, first.ID); n != 3 {
		t.Errorf("first camera segments after restart = %d, want 3 (closed on SIGINT)", n)
	}
	// The fake names segments by the second, so the new run may rewrite the
	// same names; watch the status instead of the segment count.
	waitFor(t, "first camera recording again", func() bool {
		st, _ := hub.Get(first.ID)
		return st.State == status.Recording && !st.Since.Before(restarted.Truncate(time.Second))
	})

	// Disabled and record_mode off both stop the recorder.
	off := second
	off.RecordMode = camera.RecordOff
	s.Apply([]camera.Camera{moved, off})
	if _, ok := hub.Get(second.ID); ok {
		t.Error("record_mode off camera still has a status")
	}
	disabled := moved
	disabled.Enabled = false
	s.Apply([]camera.Camera{disabled, off})
	if all := hub.All(); len(all) != 0 {
		t.Errorf("statuses after disabling everything = %+v", all)
	}

	cancel()
	<-done
	s.Apply([]camera.Camera{first}) // after shutdown: no-op, no panic

	var seen []string
	for {
		select {
		case u := <-updates:
			seen = append(seen, fmt.Sprintf("%d:%s", u.CameraID, u.State))
			continue
		default:
		}
		break
	}
	got := strings.Join(seen, " ")
	for _, want := range []string{"1:starting 1:recording", "2:starting 2:recording", "1:stopped 1:starting 1:recording", "2:stopped"} {
		if !strings.Contains(got, want) {
			t.Errorf("status updates %q lack %q\n%s", got, want, logs)
		}
	}
	if strings.Count(got, "1:stopped") != 2 {
		t.Errorf("camera 1 stopped %d times, want 2 (URL change, disable): %s", strings.Count(got, "1:stopped"), got)
	}
}

func TestStatusRetryingThenOffline(t *testing.T) {
	s, c, _ := setup(t, "fail")
	hub := status.NewHub()
	s.Status = hub
	stop := start(t, s, c)
	waitFor(t, "offline status", func() bool { st, _ := hub.Get(c.ID); return st.State == status.Offline })
	st, _ := hub.Get(c.ID)
	if st.Failures < offlineAfter || !strings.Contains(st.Error, "Connection refused") || strings.Contains(st.Error, "hunter2") {
		t.Errorf("offline status = %+v", st)
	}
	stop()
}
