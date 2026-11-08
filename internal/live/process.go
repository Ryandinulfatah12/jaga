// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package live

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
)

// Process runs go2rtc in embedded mode and restarts it when it exits.
type Process struct {
	Path string // go2rtc binary
	Dir  string // where go2rtc.yaml is written (data_dir)
	Log  *slog.Logger
	// BackoffMax caps the restart delay (default 60 s).
	BackoffMax time.Duration
	// extraEnv is added to go2rtc's environment (tests use it for the fake).
	extraEnv []string
}

// Run starts go2rtc with cfg and keeps it running until ctx is done.
// Sending a new config on reload rewrites the file and restarts go2rtc,
// which is how camera changes reach live view.
func (p *Process) Run(ctx context.Context, cfg Config, reload <-chan Config) {
	backoff := time.Second
	maxBackoff := p.BackoffMax
	if maxBackoff == 0 {
		maxBackoff = time.Minute
	}
	for {
		path, err := WriteConfig(p.Dir, cfg)
		if err != nil {
			p.Log.Error("go2rtc config", "err", err)
			return
		}
		started := time.Now()
		done, stop := p.start(ctx, path, cfg.Env)
		select {
		case <-ctx.Done():
			stop()
			if err := <-done; err != nil {
				p.Log.Debug("go2rtc stopped", "err", err) // we stopped it, so a signal exit is expected
			}
			return
		case cfg = <-reload:
			p.Log.Info("go2rtc reloading", "streams", len(cfg.Streams))
			stop()
			if err := <-done; err != nil {
				p.Log.Debug("go2rtc stopped for reload", "err", err)
			}
			backoff = time.Second
			continue
		case err := <-done:
			if time.Since(started) > maxBackoff {
				backoff = time.Second // it ran for a while: start the backoff over
			}
			p.Log.Warn("go2rtc exited, restarting", "err", err, "in", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case cfg = <-reload:
		case <-time.After(backoff):
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

// start launches go2rtc. stop asks it to quit (SIGINT, SIGKILL after 5 s).
func (p *Process) start(ctx context.Context, config string, env []string) (done <-chan error, stop func()) {
	ch := make(chan error, 1)
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, p.Path, "-config", config) //nolint:gosec // path is the admin-configured go2rtc.path
	// Only what go2rtc needs: PATH (it spawns ffmpeg for some sources), HOME,
	// TZ, and the camera URLs. Nothing else from jaga's environment, notably
	// not JAGA_SECRET_KEY.
	cmd.Env = append(append(keep(os.Environ(), "PATH", "HOME", "TZ"), p.extraEnv...), env...)
	cmd.Dir = p.Dir
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdout = io.Discard
	cmd.Stderr = &logWriter{log: p.Log}
	if err := cmd.Start(); err != nil {
		cancel()
		ch <- err
		return ch, func() {}
	}
	p.Log.Debug("go2rtc started", "pid", cmd.Process.Pid)
	go func() { ch <- cmd.Wait() }()
	return ch, cancel
}

func keep(environ []string, names ...string) []string {
	var out []string
	for _, kv := range environ {
		for _, n := range names {
			if strings.HasPrefix(kv, n+"=") {
				out = append(out, kv)
			}
		}
	}
	return out
}

// logWriter forwards go2rtc's stderr lines to the Jaga log. go2rtc already
// masks credentials in URLs it logs; userinfo is masked again here in case a
// line slips through.
type logWriter struct {
	log  *slog.Logger
	part string
}

func (w *logWriter) Write(b []byte) (int, error) {
	w.part += string(b)
	for {
		line, rest, ok := strings.Cut(w.part, "\n")
		if !ok {
			break
		}
		w.part = rest
		if line = strings.TrimSpace(line); line != "" {
			w.log.Warn("go2rtc", "line", camera.MaskText(line))
		}
	}
	if len(w.part) > 4096 {
		w.part = ""
	}
	return len(b), nil
}
