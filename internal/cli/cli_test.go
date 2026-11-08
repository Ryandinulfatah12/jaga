// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/config"
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
		{"probe without url", nil, []string{"probe"}, exitError},
		{"probe bad url", nil, []string{"probe", "file:///etc/passwd"}, exitError},
		{"probe two urls", nil, []string{"probe", "rtsp://a/s", "rtsp://b/s"}, exitError},
		{"probe bad seconds", nil, []string{"probe", "rtsp://a/s", "--seconds", "0"}, exitError},
		{"probe missing ffmpeg", map[string]string{"JAGA_FFMPEG_PATH": "definitely-not-ffmpeg"}, []string{"probe", "rtsp://a/s"}, exitDeps},
		{"user no action", nil, []string{"user"}, exitError},
		{"user bad action", nil, []string{"user", "rm", "ryan"}, exitError},
		{"user no name", nil, []string{"user", "add"}, exitError},
		{"user bad role", nil, []string{"user", "add", "ryan", "--role", "root"}, exitError},
		{"user bad name", nil, []string{"user", "add", "has space"}, exitError},
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
	env := map[string]string{"JAGA_FFMPEG_PATH": self, "JAGA_DATA_DIR": t.TempDir(), "JAGA_SECRET_KEY": testSecret, "JAGA_LISTEN": "127.0.0.1:0"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	// No admin yet: start refuses (no default account).
	var errb bytes.Buffer
	if code := Run(t.Context(), []string{"start"}, io.Discard, &errb, lookup); code != exitConfig || !strings.Contains(errb.String(), "jaga user add") {
		t.Fatalf("start without admin: exit %d\n%s", code, errb.String())
	}
	stdin = strings.NewReader("admin-pass\n")
	t.Cleanup(func() { stdin = os.Stdin })
	if code := Run(t.Context(), []string{"user", "add", "ryan"}, io.Discard, &errb, lookup); code != exitOK {
		t.Fatalf("user add: exit %d\n%s", code, errb.String())
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cancel once start logs "ready", so a slow runner cannot cancel too early.
	logs := &readyWriter{ready: cancel}

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

func TestProbeHidesPassword(t *testing.T) {
	code, out, stderr := run(t, t.Context(), nil, "probe", "rtsp://admin:hunter2@cam:99999/s")
	if code != exitError || strings.Contains(out+stderr, "hunter2") {
		t.Errorf("exit %d, output leaks password or wrong code:\n%s%s", code, out, stderr)
	}
}

func TestUserAddAndPasswd(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { stdin = os.Stdin })
	env := map[string]string{"JAGA_DATA_DIR": dir}
	user := func(input string, args ...string) (int, string) {
		stdin = strings.NewReader(input)
		var errb bytes.Buffer
		code := Run(t.Context(), append([]string{"user"}, args...), io.Discard, &errb, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
		return code, errb.String()
	}
	if code, out := user("short\n", "add", "ryan"); code != exitError || !strings.Contains(out, "8-128") {
		t.Errorf("short password: %d %s", code, out)
	}
	if code, out := user("", "add", "ryan"); code != exitError {
		t.Errorf("empty stdin: %d %s", code, out)
	}
	if code, out := user("first-pass\n", "add", "ryan", "--role", "viewer"); code != exitOK || !strings.Contains(out, "role=viewer") {
		t.Fatalf("add: %d %s", code, out)
	}
	if code, out := user("first-pass\n", "add", "RYAN"); code != exitError || !strings.Contains(out, "already in use") {
		t.Errorf("duplicate: %d %s", code, out)
	}
	if code, out := user("second-pass", "passwd", "ryan"); code != exitOK || strings.Contains(out, "second-pass") { // no trailing newline is fine
		t.Errorf("passwd: %d %s", code, out)
	}
	if code, _ := user("second-pass\n", "passwd", "nobody"); code != exitError {
		t.Errorf("passwd unknown user: %d", code)
	}
}

func TestInitThenDoctor(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Cleanup(func() { stdin = os.Stdin })
	data := filepath.Join(t.TempDir(), "rec")
	env := map[string]string{
		"JAGA_FFMPEG_PATH": "definitely-not-ffmpeg", "JAGA_GO2RTC_PATH": "definitely-not-go2rtc",
		"JAGA_LISTEN": "127.0.0.1:" + freePort(t), "JAGA_GO2RTC_API": "127.0.0.1:" + freePort(t), "JAGA_GO2RTC_WEBRTC_PORT": freePort(t),
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	cli := func(input string, args ...string) (int, string, string) {
		stdin = strings.NewReader(input)
		var out, errb bytes.Buffer
		code := Run(t.Context(), args, &out, &errb, lookup)
		return code, out.String(), errb.String()
	}

	// Before init: no secret, no database.
	code, out, _ := cli("", "doctor")
	if code != exitConfig || !strings.Contains(out, "✗ secret_key") || !strings.Contains(out, "✗ database") || !strings.Contains(out, "jaga init") {
		t.Errorf("doctor before init: exit %d\n%s", code, out)
	}

	if code, _, errb := cli("short\n", "init", "--data-dir", data); code != exitError || !strings.Contains(errb, "8-128") {
		t.Errorf("init with short password: exit %d %s", code, errb)
	}
	if _, err := os.Stat("jaga.yml"); err == nil {
		t.Fatal("a rejected init left jaga.yml behind")
	}
	if code, _, errb := cli("long-enough\n", "init", "--admin", "has space"); code != exitError || !strings.Contains(errb, "--admin") {
		t.Errorf("init with bad admin name: exit %d %s", code, errb)
	}
	code, out, errb := cli("long-enough\n", "init", "--data-dir", data, "--admin", "ryan")
	if code != exitOK || !strings.Contains(out, `admin "ryan" created`) {
		t.Fatalf("init: exit %d\n%s%s", code, out, errb)
	}
	fi, err := os.Stat("jaga.yml")
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("jaga.yml mode = %v %v", fi.Mode().Perm(), err)
	}
	b, _ := os.ReadFile("jaga.yml")
	if strings.Contains(string(b), "long-enough") {
		t.Error("password written to jaga.yml")
	}
	// The written file loads, and the secret is good enough for the sealer.
	cfg, _, err := config.Load("jaga.yml", func(string) (string, bool) { return "", false })
	if err != nil || len(cfg.SecretKey) < camera.MinSecretLen || cfg.DataDir != data {
		t.Fatalf("config after init = %+v %v", cfg, err)
	}
	if code, _, errb := cli("long-enough\n", "init"); code != exitError || !strings.Contains(errb, "already exists") {
		t.Errorf("second init: exit %d %s", code, errb)
	}
	if b2, _ := os.ReadFile("jaga.yml"); string(b2) != string(b) {
		t.Error("second init changed jaga.yml")
	}

	// After init, with a fake ffmpeg: only the missing go2rtc fails.
	env["JAGA_FFMPEG_PATH"] = self
	code, out, _ = cli("", "doctor")
	for _, want := range []string{"✓ config", "✓ secret_key", "✓ data_dir", "writable", "✓ database", "admin present", "✗ go2rtc", "live view disabled", "✓ port"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor after init lacks %q:\n%s", want, out)
		}
	}
	if code != exitDeps {
		t.Errorf("doctor exit %d, want %d (go2rtc missing)\n%s", code, exitDeps, out)
	}
	if strings.Contains(out, cfg.SecretKey) {
		t.Error("doctor printed the secret")
	}
	if left, _ := filepath.Glob(filepath.Join(data, ".jaga-doctor-*")); len(left) != 0 {
		t.Errorf("doctor left files: %v", left)
	}
}

func TestDoctorPortInUse(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	c := portCheck(t.Context(), "tcp", ln.Addr().String(), "listen", "hint")
	if c.ok || !strings.Contains(c.detail, "in use") {
		t.Errorf("taken port = %+v", c)
	}
	if c := portCheck(t.Context(), "udp", "127.0.0.1:0", "webrtc", ""); !c.ok {
		t.Errorf("free udp port = %+v", c)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[uint64]string{512: "512 B", 1500: "1.5 kB", 412_000_000_000: "412.0 GB", 2_000_000_000_000: "2.0 TB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

// freePort returns a TCP port that was free a moment ago.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	return p
}
