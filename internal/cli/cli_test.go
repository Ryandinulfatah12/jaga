// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// run executes the CLI with a fake environment and no config file on disk.
func run(t *testing.T, ctx context.Context, envm map[string]string, args ...string) (int, string, string) {
	t.Helper()
	t.Chdir(t.TempDir()) // so ./jaga.yml from the repo or host is never picked up
	var out, errb bytes.Buffer
	lookup := func(k string) (string, bool) { v, ok := envm[k]; return v, ok }
	code := Run(ctx, args, &out, &errb, lookup)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, t.Context(), map[string]string{"JAGA_FFMPEG_PATH": "definitely-not-ffmpeg"}, "version")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out, "jaga ") || !strings.Contains(out, "ffmpeg not found (definitely-not-ffmpeg)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestMigrateCreatesDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	code, _, stderr := run(t, t.Context(), nil, "--data-dir", dir, "migrate")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "jaga.db")); err != nil {
		t.Fatal(err)
	}
}

func TestFlagAfterCommand(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := run(t, t.Context(), nil, "migrate", "--data-dir", dir, "--log-format", "json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(stderr), "{") {
		t.Errorf("want JSON log, got %q", stderr)
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want int
	}{
		{"unknown command", nil, []string{"fly"}, exitError},
		{"unknown flag", nil, []string{"--nope"}, exitError},
		{"extra argument", nil, []string{"version", "extra"}, exitError},
		{"bad log level", nil, []string{"--log-level", "loud", "version"}, exitError},
		{"invalid config", map[string]string{"JAGA_STORAGE_DISK_QUOTA_PERCENT": "0"}, []string{"migrate"}, exitConfig},
		{"missing ffmpeg", map[string]string{"JAGA_FFMPEG_PATH": "definitely-not-ffmpeg", "JAGA_SECRET_KEY": testSecret}, []string{"start"}, exitDeps},
		{"missing secret", nil, []string{"start"}, exitConfig},
		{"short secret", map[string]string{"JAGA_SECRET_KEY": "short"}, []string{"start"}, exitConfig},
		{"help", nil, []string{"--help"}, exitOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _, stderr := run(t, t.Context(), tc.env, tc.args...); code != tc.want {
				t.Errorf("exit %d, want %d\n%s", code, tc.want, stderr)
			}
		})
	}
}

func TestStartStopsOnCancel(t *testing.T) {
	// Any executable stands in for ffmpeg: start only checks that it exists.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cancel once start logs "ready", so a slow runner cannot cancel too early.
	logs := &readyWriter{ready: cancel}
	env := map[string]string{"JAGA_FFMPEG_PATH": self, "JAGA_DATA_DIR": t.TempDir(), "JAGA_SECRET_KEY": testSecret}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	done := make(chan int, 1)
	go func() { done <- Run(ctx, []string{"start"}, io.Discard, logs, lookup) }()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, logs.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("start did not return after cancel")
	}
	if !strings.Contains(logs.String(), "shutting down") {
		t.Errorf("log:\n%s", logs.String())
	}
}

// readyWriter records logs and calls ready the first time "msg=ready" is written.
type readyWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	ready func()
}

func (w *readyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if w.ready != nil && strings.Contains(w.buf.String(), "msg=ready") {
		w.ready()
		w.ready = nil
	}
	return n, err
}

func (w *readyWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}
