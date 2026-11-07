// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package cli implements the jaga subcommands.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/config"
	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/internal/version"
	"github.com/ryandinulfatah12/jaga/migrations"
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
  migrate   apply database migrations and exit
  version   print version and detected dependencies

Flags:
  --config <path>      config file (default: $JAGA_CONFIG, ./jaga.yml, /etc/jaga/jaga.yml)
  --data-dir <path>    override data_dir
  --log-level <level>  debug|info|warn|error (default info)
  --log-format <fmt>   text|json (default text)
`

type globals struct {
	config, dataDir, logLevel, logFormat string
}

// register binds the global flags to fs. Defaults are the current values, so
// flags given before the command survive being registered again after it.
func (g *globals) register(fs *flag.FlagSet) {
	fs.StringVar(&g.config, "config", g.config, "")
	fs.StringVar(&g.dataDir, "data-dir", g.dataDir, "")
	fs.StringVar(&g.logLevel, "log-level", g.logLevel, "")
	fs.StringVar(&g.logFormat, "log-format", g.logFormat, "")
}

type env struct {
	ctx            context.Context
	stdout, stderr io.Writer
	lookup         func(string) (string, bool)
	g              globals
	log            *slog.Logger
}

// Run executes jaga with args (without the program name) and returns the exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, lookup func(string) (string, bool)) int {
	e := &env{ctx: ctx, stdout: stdout, stderr: stderr, lookup: lookup, g: globals{logLevel: "info", logFormat: "text"}}

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
func (e *env) openDB(cfg config.Config) (closeDB func(), code int) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		e.log.Error("data_dir", "err", err)
		return nil, exitError
	}
	conn, err := db.Open(e.ctx, filepath.Join(cfg.DataDir, "jaga.db"))
	if err != nil {
		e.log.Error("database", "err", err)
		return nil, exitError
	}
	closeDB = func() {
		if err := conn.Close(); err != nil {
			e.log.Error("close database", "err", err)
		}
	}
	if err := db.Migrate(e.ctx, conn, migrations.FS); err != nil {
		closeDB()
		e.log.Error("migrate", "err", err)
		return nil, exitError
	}
	return closeDB, exitOK
}

func (e *env) migrate() int {
	cfg, code := e.loadConfig()
	if code != exitOK {
		return code
	}
	closeDB, code := e.openDB(cfg)
	if code != exitOK {
		return code
	}
	closeDB()
	e.log.Info("migrations applied", "db", filepath.Join(cfg.DataDir, "jaga.db"))
	return exitOK
}

// start runs until ctx is cancelled (SIGINT/SIGTERM in main).
// ponytail: only config, deps and DB for now; API, supervisor and retention
// get wired here as their F1/F2 tasks land.
func (e *env) start() int {
	cfg, code := e.loadConfig()
	if code != exitOK {
		return code
	}
	if _, err := exec.LookPath(cfg.FFmpegPath); err != nil {
		e.log.Error("ffmpeg not found, install it or set ffmpeg_path", "ffmpeg_path", cfg.FFmpegPath)
		return exitDeps
	}
	closeDB, code := e.openDB(cfg)
	if code != exitOK {
		return code
	}
	defer closeDB()

	e.log.Info("ready", "version", version.Version, "data_dir", cfg.DataDir, "listen", cfg.Listen)
	<-e.ctx.Done()
	e.log.Info("shutting down")
	return exitOK
}

func (e *env) version() int {
	_, _ = fmt.Fprintln(e.stdout, version.String())

	// Config problems must not hide the version, so fall back to defaults.
	cfg, _, err := config.Load(e.g.config, e.lookup)
	if err != nil {
		cfg = config.Default()
	}
	_, _ = fmt.Fprintln(e.stdout, ffmpegVersion(e.ctx, cfg.FFmpegPath))
	return exitOK
}

// ffmpegVersion returns e.g. "ffmpeg 7.1 (/usr/bin/ffmpeg)" or "ffmpeg not found".
func ffmpegVersion(ctx context.Context, name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return "ffmpeg not found (" + name + ")"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-version").Output() //nolint:gosec // path is the admin-configured ffmpeg_path
	if err != nil {
		return "ffmpeg unusable (" + path + "): " + err.Error()
	}
	first, _, _ := strings.Cut(string(out), "\n")
	f := strings.Fields(first) // "ffmpeg version 7.1 Copyright ..."
	v := "unknown"
	if len(f) >= 3 && f[1] == "version" {
		v = f[2]
	}
	return fmt.Sprintf("ffmpeg %s (%s)", v, path)
}
