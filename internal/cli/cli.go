// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package cli implements the jaga subcommands.
package cli

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/api"
	"github.com/ryandinulfatah12/jaga/internal/auth"
	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/config"
	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/internal/live"
	"github.com/ryandinulfatah12/jaga/internal/probe"
	"github.com/ryandinulfatah12/jaga/internal/recorder"
	"github.com/ryandinulfatah12/jaga/internal/retention"
	"github.com/ryandinulfatah12/jaga/internal/status"
	"github.com/ryandinulfatah12/jaga/internal/supervisor"
	"github.com/ryandinulfatah12/jaga/internal/version"
	"github.com/ryandinulfatah12/jaga/migrations"
	"github.com/ryandinulfatah12/jaga/web"
)

// Exit codes, see docs/08-cli.md.
const (
	exitOK     = 0
	exitError  = 1
	exitConfig = 2
	exitDeps   = 3
)

const usage = `Usage: jaga [flags] <command>

Commands:
  start     run the server (default)
  init      init [--admin <name>]: write jaga.yml with a new secret_key, create the first admin
  doctor    check ffmpeg, go2rtc, data_dir, disk, ports and the admin user (changes nothing)
  migrate   apply database migrations and exit
  user      user add <name> [--role admin|viewer] | user passwd <name>
            (password read from stdin, hidden when it is a terminal)
  probe     probe <rtsp-url> [--seconds N]: codec, resolution, fps, bitrate (nothing stored)
  version   print version and detected dependencies

Flags:
  --config <path>      config file (default: $JAGA_CONFIG, ./jaga.yml, /etc/jaga/jaga.yml)
  --data-dir <path>    override data_dir
  --log-level <level>  debug|info|warn|error (default info)
  --log-format <fmt>   text|json (default text)
`

type globals struct {
	config, dataDir, logLevel, logFormat string
	seconds                              int    // probe only
	role                                 string // user add only
	admin                                string // init only
}

// register binds the global flags to fs. Defaults are the current values, so
// flags given before the command survive being registered again after it.
func (g *globals) register(fs *flag.FlagSet) {
	fs.StringVar(&g.config, "config", g.config, "")
	fs.StringVar(&g.dataDir, "data-dir", g.dataDir, "")
	fs.StringVar(&g.logLevel, "log-level", g.logLevel, "")
	fs.StringVar(&g.logFormat, "log-format", g.logFormat, "")
	fs.IntVar(&g.seconds, "seconds", g.seconds, "")
	fs.StringVar(&g.role, "role", g.role, "")
	fs.StringVar(&g.admin, "admin", g.admin, "")
}

// stdin is read by `jaga user` for the password. A variable so tests can feed it.
var stdin io.Reader = os.Stdin

type env struct {
	ctx            context.Context
	stdout, stderr io.Writer
	lookup         func(string) (string, bool)
	g              globals
	log            *slog.Logger
}

// Run executes jaga with args (without the program name) and returns the exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, lookup func(string) (string, bool)) int {
	e := &env{ctx: ctx, stdout: stdout, stderr: stderr, lookup: lookup, g: globals{logLevel: "info", logFormat: "text", seconds: 5, role: auth.RoleAdmin, admin: "admin"}}

	root := newFlagSet("jaga")
	e.g.register(root)
	if err := root.Parse(args); err != nil {
		return usageError(stderr, err)
	}
	cmd, rest := "start", root.Args()
	if len(rest) > 0 {
		cmd, rest = rest[0], rest[1:]
	}
	sub := newFlagSet(cmd)
	e.g.register(sub)
	if err := sub.Parse(rest); err != nil {
		return usageError(stderr, err)
	}
	// probe <url> and user <action> <name> take positional arguments;
	// flags may follow them.
	var pos []string
	want := map[string]int{"probe": 1, "user": 2}[cmd]
	for len(pos) < want && sub.NArg() > 0 {
		pos = append(pos, sub.Arg(0))
		if err := sub.Parse(sub.Args()[1:]); err != nil {
			return usageError(stderr, err)
		}
	}
	if sub.NArg() > 0 {
		return usageError(stderr, fmt.Errorf("%s: unexpected argument %q", cmd, sub.Arg(0)))
	}

	log, err := newLogger(stderr, e.g.logLevel, e.g.logFormat)
	if err != nil {
		return usageError(stderr, err)
	}
	e.log = log

	switch cmd {
	case "start":
		return e.start()
	case "migrate":
		return e.migrate()
	case "version":
		return e.version()
	case "init":
		return e.initCmd()
	case "doctor":
		return e.doctor()
	case "probe":
		if len(pos) < 1 {
			return usageError(stderr, errors.New("probe: missing <rtsp-url>"))
		}
		return e.probe(pos[0])
	case "user":
		if len(pos) < 2 || (pos[0] != "add" && pos[0] != "passwd") {
			return usageError(stderr, errors.New("user: want `user add <name>` or `user passwd <name>`"))
		}
		return e.user(pos[0], pos[1])
	case "help":
		_, _ = fmt.Fprint(stdout, usage)
		return exitOK
	default:
		return usageError(stderr, fmt.Errorf("unknown command %q", cmd))
	}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // usage is printed once, by usageError
	return fs
}

func usageError(stderr io.Writer, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		_, _ = fmt.Fprint(stderr, usage)
		return exitOK
	}
	_, _ = fmt.Fprintf(stderr, "jaga: %v\n\n%s", err, usage)
	return exitError
}

func newLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("--log-level %q: want debug, info, warn or error", level)
	}
	opts := &slog.HandlerOptions{Level: lv}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("--log-format %q: want text or json", format)
}

// loadConfig applies --data-dir on top of the file and env.
func (e *env) loadConfig() (config.Config, int) {
	cfg, path, err := config.Load(e.g.config, e.lookup)
	if err == nil && e.g.dataDir != "" {
		cfg.DataDir = e.g.dataDir
	}
	if err != nil {
		e.log.Error("config", "err", err)
		return cfg, exitConfig
	}
	if path == "" {
		e.log.Debug("no config file, using defaults and JAGA_* env")
	} else {
		e.log.Debug("config loaded", "path", path)
	}
	return cfg, exitOK
}

// openDB creates data_dir if needed, opens jaga.db and applies migrations.
func (e *env) openDB(cfg config.Config) (conn *sql.DB, closeDB func(), code int) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		e.log.Error("data_dir", "err", err)
		return nil, nil, exitError
	}
	conn, err := db.Open(e.ctx, filepath.Join(cfg.DataDir, "jaga.db"))
	if err != nil {
		e.log.Error("database", "err", err)
		return nil, nil, exitError
	}
	closeDB = func() {
		if err := conn.Close(); err != nil {
			e.log.Error("close database", "err", err)
		}
	}
	if err := db.Migrate(e.ctx, conn, migrations.FS); err != nil {
		closeDB()
		e.log.Error("migrate", "err", err)
		return nil, nil, exitError
	}
	return conn, closeDB, exitOK
}

func (e *env) migrate() int {
	cfg, code := e.loadConfig()
	if code != exitOK {
		return code
	}
	_, closeDB, code := e.openDB(cfg)
	if code != exitOK {
		return code
	}
	closeDB()
	e.log.Info("migrations applied", "db", filepath.Join(cfg.DataDir, "jaga.db"))
	return exitOK
}

// start runs until ctx is cancelled (SIGINT/SIGTERM in main): HTTP API,
// recording supervisor and retention.
func (e *env) start() int {
	cfg, code := e.loadConfig()
	if code != exitOK {
		return code
	}
	// Camera URLs are encrypted with secret_key, so start refuses to run
	// without one (docs/03-design.md, Keamanan).
	seal, err := camera.NewSealer(cfg.SecretKey)
	if err != nil {
		e.log.Error("set JAGA_SECRET_KEY (e.g. openssl rand -base64 32)", "err", err)
		return exitConfig
	}
	ffmpeg, err := exec.LookPath(cfg.FFmpegPath)
	if err != nil {
		e.log.Error("ffmpeg not found, install it or set ffmpeg_path", "ffmpeg_path", cfg.FFmpegPath)
		return exitDeps
	}
	conn, closeDB, code := e.openDB(cfg)
	if code != exitOK {
		return code
	}
	defer closeDB()

	users := auth.NewStore(conn)
	// No default account (docs/03-design.md, Keamanan).
	if ok, err := users.HasAdmin(e.ctx); err != nil {
		e.log.Error("users", "err", err)
		return exitError
	} else if !ok {
		e.log.Error("no admin user yet, create one with: jaga user add <name>")
		return exitConfig
	}
	cams := camera.NewStore(conn, seal)
	all, err := cams.List(e.ctx)
	if err != nil {
		e.log.Error("load cameras", "err", err)
		return exitError
	}
	rec := 0
	for _, c := range all {
		if c.Enabled && c.RecordMode == camera.RecordContinuous {
			rec++
		}
	}
	hub := status.NewHub()
	sup := &supervisor.Supervisor{FFmpegPath: ffmpeg, DataDir: cfg.DataDir, DB: conn, Log: e.log, Status: hub}
	ret := &retention.Job{
		DB: conn, DataDir: cfg.DataDir, Log: e.log,
		DefaultDays:  cfg.Storage.DefaultRetentionDays,
		QuotaPercent: cfg.Storage.DiskQuotaPercent,
		Probe: func(ctx context.Context, path string) (time.Duration, error) {
			return recorder.Duration(ctx, recorder.FFprobePath(ffmpeg), path)
		},
	}

	// Listen before "ready" so a taken port fails start instead of a goroutine.
	ln, err := (&net.ListenConfig{}).Listen(e.ctx, "tcp", cfg.Listen)
	if err != nil {
		e.log.Error("listen", "addr", cfg.Listen, "err", err)
		return exitError
	}
	lv := e.liveView(cfg, all)
	apiSrv := &api.Server{Auth: users, Cameras: cams, Log: e.log, FFprobe: recorder.FFprobePath(ffmpeg), Status: hub, UI: web.Handler(),
		DB: conn, DataDir: cfg.DataDir, FFmpeg: ffmpeg}
	if lv != nil {
		apiSrv.Live = lv.proxy
	}
	apiSrv.CamerasChanged = camerasChanged(e.ctx, e.log, cams, sup, lv)
	srv := &http.Server{
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(e.log.Handler(), slog.LevelWarn),
	}

	e.log.Info("ready", "version", version.Version, "data_dir", cfg.DataDir, "listen", ln.Addr().String(),
		"cameras", len(all), "recording", rec)
	var wg sync.WaitGroup
	wg.Go(func() { ret.Run(e.ctx) })
	if lv != nil && lv.proc != nil {
		wg.Go(func() { lv.proc.Run(e.ctx, lv.first, lv.reload) })
	}
	wg.Go(func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			e.log.Error("http", "err", err)
		}
	})
	wg.Go(func() {
		<-e.ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx) // in-flight requests get 5 s
	})
	sup.Run(e.ctx, all) // returns after ctx is cancelled and every recorder has stopped
	wg.Wait()
	e.log.Info("shutting down")
	return exitOK
}

// liveState wires go2rtc into start.
type liveState struct {
	proxy  *live.Proxy
	proc   *live.Process // nil in external mode
	first  live.Config
	reload chan live.Config
	cfg    config.Config
	log    *slog.Logger
}

// liveView returns nil when live view cannot run. That only disables live
// view and snapshots, never recording.
func (e *env) liveView(cfg config.Config, cams []camera.Camera) *liveState {
	lc, err := live.BuildConfig(cfg.Go2RTC.API, cfg.Go2RTC.WebRTCPort, cams)
	if err != nil {
		e.log.Error("live view disabled: go2rtc config", "err", err)
		return nil
	}
	lv := &liveState{proxy: live.NewProxy(cfg.Go2RTC.API), first: lc, reload: make(chan live.Config, 1), cfg: cfg, log: e.log}
	lv.proxy.SetStreams(lc.Streams)
	if cfg.Go2RTC.Mode == "external" {
		// ponytail: external go2rtc keeps its own config; stream names must
		// follow live.StreamName (cam<ID>, cam<ID>_sub). Push config through
		// its API if users ask for it.
		e.log.Info("live view via external go2rtc", "api", cfg.Go2RTC.API)
		return lv
	}
	path, err := exec.LookPath(cfg.Go2RTC.Path)
	if err != nil {
		e.log.Warn("live view disabled: go2rtc not found, install it or set go2rtc.path", "go2rtc_path", cfg.Go2RTC.Path)
		return nil
	}
	lv.proc = &live.Process{Path: path, Dir: cfg.DataDir, Log: e.log}
	return lv
}

// camerasChanged returns the API's CamerasChanged hook: it reloads the
// camera list into the recorders and live view. It returns at once and does
// the work in the background, one reload at a time, so an API request never
// waits for FFmpeg to close a segment. Changes made during a reload are
// folded into one more reload.
func camerasChanged(ctx context.Context, log *slog.Logger, cams *camera.Store, sup *supervisor.Supervisor, lv *liveState) func() {
	kick := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-kick:
			}
			all, err := cams.List(ctx)
			if err != nil {
				log.Error("camera reload", "err", err)
				continue
			}
			if lv != nil {
				lv.apply(all)
			}
			sup.Apply(all)
		}
	}()
	return func() {
		select {
		case kick <- struct{}{}:
		default: // a reload is already pending and will read the latest list
		}
	}
}

// apply rebuilds the go2rtc config and hands it over. The newest config
// wins if one is still pending.
func (lv *liveState) apply(all []camera.Camera) {
	lc, err := live.BuildConfig(lv.cfg.Go2RTC.API, lv.cfg.Go2RTC.WebRTCPort, all)
	if err != nil {
		lv.log.Error("live view reload", "err", err)
		return
	}
	lv.proxy.SetStreams(lc.Streams)
	if lv.proc == nil {
		return
	}
	select {
	case <-lv.reload: // drop the stale pending config
	default:
	}
	lv.reload <- lc
}

// user adds a user or resets a password. The password is read from stdin,
// one line, so it never shows up in argv, shell history or `ps`.
func (e *env) user(action, name string) int {
	cfg, code := e.loadConfig()
	if code != exitOK {
		return code
	}
	if action == "add" {
		if err := auth.ValidateUser(name, "placeholder", e.g.role); err != nil {
			return usageError(e.stderr, err) // bad name or role: say so before asking for a password
		}
	}
	pw, err := readPassword(e.ctx, e.stderr, "Password for "+name+": ")
	if err != nil {
		e.log.Error("read password", "err", err)
		return exitError
	}
	if err := auth.ValidatePassword(pw); err != nil {
		return usageError(e.stderr, err)
	}
	conn, closeDB, code := e.openDB(cfg)
	if code != exitOK {
		return code
	}
	defer closeDB()
	users := auth.NewStore(conn)
	if action == "add" {
		u, err := users.CreateUser(e.ctx, name, pw, e.g.role)
		if err != nil {
			e.log.Error("user add", "err", err)
			return exitError
		}
		e.log.Info("user added", "user", u.Username, "role", u.Role)
		return exitOK
	}
	if err := users.SetPassword(e.ctx, name, pw); err != nil {
		e.log.Error("user passwd", "err", err)
		return exitError
	}
	e.log.Info("password changed, sessions ended", "user", name)
	return exitOK
}

// readPassword reads one line from stdin. On a terminal it turns echo off
// with stty for the duration.
// ponytail: stty works on Linux/macOS; golang.org/x/term would cover
// Windows too, add it if Windows builds are ever shipped.
func readPassword(ctx context.Context, prompt io.Writer, label string) (string, error) {
	if f, ok := stdin.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			_, _ = fmt.Fprint(prompt, label)
			stty := func(arg string) error {
				cmd := exec.CommandContext(ctx, "stty", arg) //nolint:gosec // fixed argv
				cmd.Stdin = f                                // stty acts on its stdin, which must be the terminal
				return cmd.Run()
			}
			if stty("-echo") == nil {
				defer func() { _ = stty("echo"); _, _ = fmt.Fprintln(prompt) }()
			}
		}
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		return "", errors.New("no password on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// probe prints what ffprobe sees at url, the same check POST
// /cameras/{id}/test runs. The URL is shown with credentials masked.
func (e *env) probe(url string) int {
	// Bad input is reported before anything else, ffmpeg or not.
	if err := camera.ValidateURL(url); err != nil {
		return usageError(e.stderr, err)
	}
	if e.g.seconds < 1 || e.g.seconds > 60 {
		return usageError(e.stderr, fmt.Errorf("--seconds %d: want 1-60", e.g.seconds))
	}
	cfg, _, err := config.Load(e.g.config, e.lookup)
	if err != nil {
		cfg = config.Default() // only ffmpeg_path matters here
	}
	ffmpeg, err := exec.LookPath(cfg.FFmpegPath)
	if err != nil {
		e.log.Error("ffmpeg not found, install it or set ffmpeg_path", "ffmpeg_path", cfg.FFmpegPath)
		return exitDeps
	}
	res, err := probe.Probe(e.ctx, recorder.FFprobePath(ffmpeg), url, time.Duration(e.g.seconds)*time.Second)
	if err != nil {
		// Both error kinds already mask credentials.
		_, _ = fmt.Fprintf(e.stderr, "✗ %s\n  %v\n", camera.MaskURL(url), err)
		return exitError
	}
	_, _ = fmt.Fprintf(e.stdout, "✓ %s  (sampled %.1fs)\n", camera.MaskURL(url), res.Sampled.Seconds())
	for _, s := range res.Streams {
		_, _ = fmt.Fprintln(e.stdout, "  "+describe(s))
	}
	return exitOK
}

// describe formats one stream, e.g. "video  H264 1920×1080 25 fps  2.1 Mbps  keyframe 2.0s".
func describe(s probe.Stream) string {
	parts := []string{fmt.Sprintf("%-5s  %s", s.Type, strings.ToUpper(s.Codec))}
	switch s.Type {
	case "video":
		if s.Width > 0 {
			parts = append(parts, fmt.Sprintf("%d×%d", s.Width, s.Height))
		}
		if s.FPS > 0 {
			parts = append(parts, strconv.FormatFloat(s.FPS, 'f', -1, 64)+" fps")
		}
	case "audio":
		if s.SampleRate > 0 {
			parts = append(parts, fmt.Sprintf("%d Hz", s.SampleRate))
		}
		if s.Channels > 0 {
			parts = append(parts, fmt.Sprintf("%d ch", s.Channels))
		}
	}
	switch {
	case s.BitrateKbps >= 1000:
		parts = append(parts, fmt.Sprintf(" %.1f Mbps", float64(s.BitrateKbps)/1000))
	case s.BitrateKbps > 0:
		parts = append(parts, fmt.Sprintf(" %d kbps", s.BitrateKbps))
	}
	if s.GOPSeconds > 0 {
		parts = append(parts, fmt.Sprintf(" keyframe %.1fs", s.GOPSeconds))
	}
	return strings.Join(parts, " ")
}

func (e *env) version() int {
	_, _ = fmt.Fprintln(e.stdout, version.String())

	// Config problems must not hide the version, so fall back to defaults.
	cfg, _, err := config.Load(e.g.config, e.lookup)
	if err != nil {
		cfg = config.Default()
	}
	for _, t := range [][2]string{{"ffmpeg", cfg.FFmpegPath}, {"go2rtc", cfg.Go2RTC.Path}} {
		v, err := toolVersion(e.ctx, t[0], t[1])
		if err != nil {
			v = err.Error()
		}
		_, _ = fmt.Fprintln(e.stdout, v)
	}
	return exitOK
}
