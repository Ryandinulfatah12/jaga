// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ryandinulfatah12/jaga/internal/auth"
	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/config"
	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/internal/recorder"
	"github.com/ryandinulfatah12/jaga/internal/retention"
)

// toolVersion returns e.g. "ffmpeg 7.1 (/usr/bin/ffmpeg)". Both ffmpeg and
// go2rtc print "<name> version <v> ..." on the first line of -version.
func toolVersion(ctx context.Context, tool, name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found (%s)", tool, name)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-version").Output() //nolint:gosec // admin-configured binary path
	if err != nil {
		return "", fmt.Errorf("%s unusable (%s): %w", tool, path, err)
	}
	first, _, _ := strings.Cut(string(out), "\n")
	f := strings.Fields(first)
	v := "unknown"
	if len(f) >= 3 && f[1] == "version" {
		v = f[2]
	}
	return fmt.Sprintf("%s %s (%s)", tool, v, path), nil
}

// check is one line of `jaga doctor`.
type check struct {
	ok           bool
	name, detail string
	hint         string // shown under a failed check
	code         int    // exit code when it fails
}

// doctor checks everything `jaga start` needs and prints one line per check.
// It changes nothing: no data_dir is created, no migration runs.
func (e *env) doctor() int {
	var checks []check
	add := func(c check) { checks = append(checks, c) }
	fail := func(name, detail, hint string, code int) {
		add(check{name: name, detail: detail, hint: hint, code: code})
	}

	cfg, path, err := config.Load(e.g.config, e.lookup)
	if err == nil && e.g.dataDir != "" {
		cfg.DataDir = e.g.dataDir
	}
	switch {
	case err != nil:
		fail("config", err.Error(), "fix jaga.yml or the JAGA_* variable named above", exitConfig)
		return e.report(checks) // the rest depends on a valid config
	case path == "":
		add(check{ok: true, name: "config", detail: "no jaga.yml, defaults + JAGA_* env (run `jaga init` to create one)"})
	default:
		add(check{ok: true, name: "config", detail: path})
	}
	if len(cfg.SecretKey) < camera.MinSecretLen {
		fail("secret_key", fmt.Sprintf("missing or shorter than %d characters", camera.MinSecretLen),
			"run `jaga init`, or set JAGA_SECRET_KEY (openssl rand -base64 32)", exitConfig)
	} else {
		add(check{ok: true, name: "secret_key", detail: "set"})
	}

	if v, err := toolVersion(e.ctx, "ffmpeg", cfg.FFmpegPath); err != nil {
		fail("ffmpeg", err.Error(), "install ffmpeg or set ffmpeg_path", exitDeps)
	} else {
		add(check{ok: true, name: "ffmpeg", detail: v})
		ffmpeg, _ := exec.LookPath(cfg.FFmpegPath)
		if _, err := exec.LookPath(recorder.FFprobePath(ffmpeg)); err != nil {
			fail("ffprobe", "not found next to ffmpeg", "install ffprobe (same package as ffmpeg)", exitDeps)
		} else {
			add(check{ok: true, name: "ffprobe", detail: "found"})
		}
	}

	if cfg.Go2RTC.Mode == "external" {
		if err := ping(e.ctx, "http://"+cfg.Go2RTC.API+"/api"); err != nil {
			fail("go2rtc", "external at "+cfg.Go2RTC.API+": "+err.Error(), "start go2rtc, or set go2rtc.mode: embedded", exitError)
		} else {
			add(check{ok: true, name: "go2rtc", detail: "external at " + cfg.Go2RTC.API})
		}
	} else if v, err := toolVersion(e.ctx, "go2rtc", cfg.Go2RTC.Path); err != nil {
		// Only live view needs it; recording works without.
		fail("go2rtc", err.Error()+", live view disabled", "install go2rtc or set go2rtc.path", exitDeps)
	} else {
		add(check{ok: true, name: "go2rtc", detail: v})
	}

	add(dataDirCheck(cfg))
	add(dbCheck(e.ctx, cfg.DataDir))

	add(portCheck(e.ctx, "tcp", cfg.Listen, "listen", "stop what uses it or set listen in jaga.yml"))
	if cfg.Go2RTC.Mode == "embedded" {
		addr := fmt.Sprintf(":%d", cfg.Go2RTC.WebRTCPort)
		hint := "stop the other program or set go2rtc.webrtc_port in jaga.yml"
		add(portCheck(e.ctx, "tcp", addr, "webrtc", hint))
		add(portCheck(e.ctx, "udp", addr, "webrtc", hint))
		add(portCheck(e.ctx, "tcp", cfg.Go2RTC.API, "go2rtc api", "stop the other go2rtc or set go2rtc.api in jaga.yml"))
	}
	return e.report(checks)
}

func (e *env) report(checks []check) int {
	code := exitOK
	for _, c := range checks {
		mark := "✓"
		if !c.ok {
			mark = "✗"
			if code == exitOK {
				code = c.code // checks run in fix-this-first order: config before tools
			}
		}
		_, _ = fmt.Fprintf(e.stdout, "%s %-12s %s\n", mark, c.name, c.detail)
		if !c.ok && c.hint != "" {
			_, _ = fmt.Fprintf(e.stdout, "  → %s\n", c.hint)
		}
	}
	return code
}

func dataDirCheck(cfg config.Config) check {
	c := check{name: "data_dir", hint: "create it and make it writable by the jaga user", code: exitError}
	fi, err := os.Stat(cfg.DataDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.ok, c.detail = true, cfg.DataDir+" missing, `jaga start` creates it"
		return c
	case err != nil:
		c.detail = err.Error()
		return c
	case !fi.IsDir():
		c.detail = cfg.DataDir + " is not a directory"
		return c
	}
	f, err := os.CreateTemp(cfg.DataDir, ".jaga-doctor-*")
	if err != nil {
		c.detail = cfg.DataDir + " not writable: " + err.Error()
		return c
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	used, avail, err := retention.DiskUsage(cfg.DataDir)
	if err != nil {
		c.detail = err.Error()
		return c
	}
	pct := 0
	if used+avail > 0 {
		pct = int(used * 100 / (used + avail)) //nolint:gosec // G115: a percentage, 0-100
	}
	c.ok = true
	c.detail = fmt.Sprintf("%s  writable  %s free (%d%% used, quota %d%%)", cfg.DataDir, humanBytes(avail), pct, cfg.Storage.DiskQuotaPercent)
	if pct >= cfg.Storage.DiskQuotaPercent {
		c.ok, c.code = false, exitError
		c.hint = "disk above storage.disk_quota_percent: retention will delete the oldest recordings"
	}
	return c
}

func dbCheck(ctx context.Context, dataDir string) check {
	c := check{name: "database", code: exitConfig, hint: "create the first admin: jaga user add <name>"}
	path := filepath.Join(dataDir, "jaga.db")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		c.detail = "no jaga.db yet"
		c.hint = "run `jaga init` (or `jaga user add <name>`) to create it with the first admin"
		return c
	}
	conn, err := db.Open(ctx, path)
	if err != nil {
		c.detail, c.code, c.hint = err.Error(), exitError, ""
		return c
	}
	defer func() { _ = conn.Close() }()
	ok, err := auth.NewStore(conn).HasAdmin(ctx)
	switch {
	case err != nil:
		c.detail, c.hint = "cannot read users: "+err.Error(), "run `jaga migrate`"
	case !ok:
		c.detail = path + ", no admin user"
	default:
		c.ok, c.detail = true, path+", admin present"
	}
	return c
}

// portCheck reports whether addr can be bound. Taken ports usually mean
// another jaga, go2rtc or NVR is already running.
func portCheck(ctx context.Context, network, addr, what, hint string) check {
	name := fmt.Sprintf("port %s/%s", portOf(addr), network)
	c := check{name: name, hint: hint, code: exitError}
	var lc net.ListenConfig
	var err error
	if network == "udp" {
		var pc net.PacketConn
		if pc, err = lc.ListenPacket(ctx, "udp", addr); err == nil {
			_ = pc.Close()
		}
	} else {
		var ln net.Listener
		if ln, err = lc.Listen(ctx, "tcp", addr); err == nil {
			_ = ln.Close()
		}
	}
	if err != nil {
		c.detail = what + ": " + err.Error()
		return c
	}
	c.ok, c.detail = true, what+" free"
	return c
}

func portOf(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return addr
}

func ping(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func humanBytes(n uint64) string {
	const unit = 1000 // df -H style, the unit disks are sold in
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTP"[exp])
}

// initConfig is what `jaga init` writes. Only the keys a new install
// usually changes; everything else keeps its default.
type initConfig struct {
	Listen    string `yaml:"listen"`
	DataDir   string `yaml:"data_dir"`
	SecretKey string `yaml:"secret_key"`
}

// init writes a new jaga.yml with a generated secret_key and creates the
// first admin. It never overwrites an existing config file.
func (e *env) initCmd() int {
	path := e.g.config
	if path == "" {
		path = "jaga.yml"
	}
	if _, err := os.Stat(path); err == nil {
		_, _ = fmt.Fprintf(e.stderr, "jaga: %s already exists, not overwriting it\n", path)
		return exitError
	}
	admin := e.g.admin
	if err := auth.ValidateUser(admin, "placeholder", auth.RoleAdmin); err != nil {
		return usageError(e.stderr, fmt.Errorf("--admin: %w", err))
	}
	def := config.Default()
	ic := initConfig{Listen: def.Listen, DataDir: def.DataDir}
	if e.g.dataDir != "" {
		ic.DataDir = e.g.dataDir
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	ic.SecretKey = base64.StdEncoding.EncodeToString(key) // 44 chars, 256 bits

	// Ask for the password before writing anything, so a typo leaves no
	// half-initialized install behind.
	pw, err := readPassword(e.ctx, e.stderr, "Password for admin "+admin+": ")
	if err != nil {
		e.log.Error("read password", "err", err)
		return exitError
	}
	if err := auth.ValidatePassword(pw); err != nil {
		return usageError(e.stderr, err)
	}

	b, _ := yaml.Marshal(ic)
	header := "# Created by `jaga init`. Every key and its default: jaga.example.yml.\n" +
		"# secret_key encrypts the camera passwords in jaga.db: back it up,\n" +
		"# without it the cameras have to be added again.\n"
	// O_EXCL: never overwrite, even if the file appeared since the check above.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is the admin's --config or ./jaga.yml
	if err != nil {
		e.log.Error("write config", "err", err)
		return exitError
	}
	if _, err := f.WriteString(header + string(b)); err != nil {
		_ = f.Close()
		e.log.Error("write config", "err", err)
		return exitError
	}
	if err := f.Close(); err != nil {
		e.log.Error("write config", "err", err)
		return exitError
	}

	cfg := def
	cfg.DataDir = ic.DataDir
	conn, closeDB, code := e.openDB(cfg)
	if code != exitOK {
		return code
	}
	defer closeDB()
	if _, err := auth.NewStore(conn).CreateUser(e.ctx, admin, pw, auth.RoleAdmin); err != nil {
		e.log.Error("create admin, config was written; retry with `jaga user add`", "err", err)
		return exitError
	}
	_, _ = fmt.Fprintf(e.stdout, "✓ %s written (mode 0600), back up its secret_key\n✓ admin %q created in %s\n  next: jaga doctor, then jaga start\n",
		path, admin, filepath.Join(cfg.DataDir, "jaga.db"))
	return exitOK
}
