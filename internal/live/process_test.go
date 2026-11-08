// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package live

import (
	"bytes"
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
)

// The test binary doubles as a fake go2rtc when JAGA_FAKE_GO2RTC is set. It
// appends "<pid> <env>" to $RUNLOG, so the test sees each start and the
// environment it got.
func TestMain(m *testing.M) {
	mode := os.Getenv("JAGA_FAKE_GO2RTC")
	if mode == "" {
		os.Exit(m.Run())
	}
	f, _ := os.OpenFile(os.Getenv("RUNLOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G703: test temp file set by the test itself
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "JAGA_CAM_") || strings.HasPrefix(kv, "JAGA_SECRET") {
			env = append(env, kv)
		}
	}
	_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), strings.Join(env, ","))
	_ = f.Close()
	if mode == "crash" {
		fmt.Fprintln(os.Stderr, "ERR rtsp://admin:hunter2@cam/s: boom")
		os.Exit(1)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	os.Exit(0)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func runs(t *testing.T, path string) []string {
	t.Helper()
	b, _ := os.ReadFile(path) //nolint:gosec // test temp file
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", " | "))
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func start(t *testing.T, mode string) (p *Process, runlog string, logs *syncBuf, reload chan Config, stop func()) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	runlog = filepath.Join(dir, "runs")
	t.Setenv("JAGA_SECRET_KEY", "must-not-leak") // in jaga's env, must not reach go2rtc
	logs = &syncBuf{}
	p = &Process{Path: self, Dir: dir, Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), BackoffMax: 200 * time.Millisecond,
		extraEnv: []string{"JAGA_FAKE_GO2RTC=" + mode, "RUNLOG=" + runlog}}
	reload = make(chan Config, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx, Config{YAML: []byte("streams: {}\n"), Env: []string{"JAGA_CAM_1=rtsp://a/s"}}, reload)
		close(done)
	}()
	stop = func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return after cancel")
		}
	}
	return p, runlog, logs, reload, stop
}

func TestProcessReloadAndStop(t *testing.T) {
	_, runlog, logs, reload, stop := start(t, "serve")
	waitFor(t, "first start", func() bool { return strings.Contains(strings.Join(runs(t, runlog), " "), "JAGA_CAM_1") })
	reload <- Config{YAML: []byte("streams: {}\n"), Env: []string{"JAGA_CAM_1=rtsp://b/s", "JAGA_CAM_2=rtsp://c/s"}}
	waitFor(t, "restart after reload", func() bool { return strings.Contains(strings.Join(runs(t, runlog), " "), "JAGA_CAM_2") })
	stop()

	all := strings.Join(runs(t, runlog), " ")
	if strings.Contains(all, "must-not-leak") || strings.Contains(all, "JAGA_SECRET_KEY") {
		t.Errorf("go2rtc got jaga's environment: %s", all)
	}
	if strings.Count(all, "|")+1 != 2 {
		t.Errorf("starts = %q, want 2", all)
	}
	if !strings.Contains(logs.String(), "go2rtc reloading") {
		t.Errorf("log:\n%s", logs)
	}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(runlog), "go2rtc.yaml")); err != nil || string(b) != "streams: {}\n" { //nolint:gosec // test temp file
		t.Errorf("config file = %q %v", b, err)
	}
}

func TestProcessRestartsAfterCrash(t *testing.T) {
	_, runlog, logs, _, stop := start(t, "crash")
	waitFor(t, "three starts", func() bool { return strings.Count(strings.Join(runs(t, runlog), " "), "|") >= 2 })
	stop()
	l := logs.String()
	if !strings.Contains(l, "go2rtc exited, restarting") || strings.Contains(l, "hunter2") || !strings.Contains(l, "rtsp://***@cam/s") {
		t.Errorf("log (want restart, masked URL):\n%s", l)
	}
}
