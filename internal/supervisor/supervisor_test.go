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
		fakeFail()
	}
}

// fakeRecord writes two segments, waits for SIGINT, then closes a third one
// like FFmpeg does on a clean stop.
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
	segment(0)
	segment(1)
	<-stop
	segment(2)
	os.Exit(255) // FFmpeg's exit code after SIGINT
}

// fakeFail counts its runs and exits like a refused camera connection.
func fakeFail() {
	f, err := os.OpenFile(os.Getenv("JAGA_FAKE_COUNT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // test fake, temp file set by the test
	if err == nil {
		_, _ = f.WriteString("run\n")
		_ = f.Close()
	}
	fmt.Fprintln(os.Stderr, "[tcp @ 0x1] Connection to tcp://cam:554 failed: Connection refused")
	fmt.Fprintln(os.Stderr, "rtsp://admin:hunter2@cam:554/stream1: Connection refused")
	os.Exit(1)
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
		RestartDelay: 20 * time.Millisecond,
	}
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

func TestRestartsAndMasksErrors(t *testing.T) {
	s, c, logs := setup(t, "fail")
	counter := filepath.Join(t.TempDir(), "runs")
	t.Setenv("JAGA_FAKE_COUNT", counter)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx, []camera.Camera{c}); close(done) }()

	waitFor(t, "three runs", func() bool {
		b, _ := os.ReadFile(counter) //nolint:gosec // temp file created by the test
		return strings.Count(string(b), "run") >= 3
	})
	cancel()
	<-done

	l := logs.String()
	if !strings.Contains(l, "recorder exited, restarting") || !strings.Contains(l, "Connection refused") {
		t.Errorf("restart reason not logged:\n%s", l)
	}
	if strings.Contains(l, "hunter2") {
		t.Errorf("camera password leaked into the log:\n%s", l)
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
	if got := ffprobePath("/usr/bin/ffmpeg"); got != "/usr/bin/ffprobe" {
		t.Errorf("ffprobePath = %q", got)
	}
	if got := ffprobePath("ffmpeg"); got != "ffprobe" {
		t.Errorf("ffprobePath bare = %q", got)
	}
	if got := maskCredentials("rtsp://admin:hunter2@cam/s: 401 and rtsps://u@x/y"); got != "rtsp://***@cam/s: 401 and rtsps://***@x/y" {
		t.Errorf("maskCredentials = %q", got)
	}
}
