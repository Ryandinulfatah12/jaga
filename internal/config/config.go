// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package config loads jaga.yml and applies JAGA_* environment overrides.
//
// Precedence, lowest first: built-in defaults, the config file, JAGA_*
// environment variables, then command-line flags (applied by the caller).
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ErrInvalid wraps every configuration problem, so callers can exit with code 2.
var ErrInvalid = errors.New("invalid config")

// searchPaths are tried in order when neither --config nor $JAGA_CONFIG is set.
// A variable so tests can point it elsewhere.
var searchPaths = []string{"jaga.yml", "/etc/jaga/jaga.yml"}

// Config mirrors jaga.yml. Field tags double as the env names:
// storage.disk_quota_percent is JAGA_STORAGE_DISK_QUOTA_PERCENT.
type Config struct {
	Listen     string  `yaml:"listen"`
	DataDir    string  `yaml:"data_dir"`
	FFmpegPath string  `yaml:"ffmpeg_path"`
	Go2RTC     Go2RTC  `yaml:"go2rtc"`
	Storage    Storage `yaml:"storage"`
	SecretKey  string  `yaml:"secret_key"`
}

// Go2RTC configures the live-view sidecar.
type Go2RTC struct {
	Mode string `yaml:"mode"` // embedded | external
	API  string `yaml:"api"`
}

// Storage configures retention.
type Storage struct {
	DiskQuotaPercent     int `yaml:"disk_quota_percent"`
	DefaultRetentionDays int `yaml:"default_retention_days"`
}

// Default returns the built-in configuration used when no file is found.
func Default() Config {
	return Config{
		Listen:     ":8080",
		DataDir:    "data",
		FFmpegPath: "ffmpeg",
		Go2RTC:     Go2RTC{Mode: "embedded", API: "127.0.0.1:1984"},
		Storage:    Storage{DiskQuotaPercent: 90, DefaultRetentionDays: 7},
	}
}

// Load builds the configuration. explicit is the --config flag ("" to search).
// lookup reads the environment (os.LookupEnv outside tests). The returned
// path is the file that was read, or "" when only defaults and env apply.
func Load(explicit string, lookup func(string) (string, bool)) (Config, string, error) {
	cfg := Default()

	path, err := find(explicit, lookup)
	if err != nil {
		return cfg, "", err
	}
	if path != "" {
		if err := readFile(path, &cfg); err != nil {
			return cfg, path, err
		}
	}
	if err := applyEnv(reflect.ValueOf(&cfg).Elem(), "JAGA", lookup); err != nil {
		return cfg, path, err
	}
	return cfg, path, cfg.Validate()
}

// find resolves the config file: --config, then $JAGA_CONFIG (both must exist),
// then the first search path that exists.
func find(explicit string, lookup func(string) (string, bool)) (string, error) {
	if explicit == "" {
		explicit, _ = lookup("JAGA_CONFIG")
	}
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return explicit, nil
	}
	for _, p := range searchPaths {
		_, err := os.Stat(p)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	return "", nil
}

func readFile(path string, cfg *Config) error {
	f, err := os.Open(path) //nolint:gosec // the path comes from the admin (flag, env or fixed search paths)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true) // a typo in a key is an error, not a silently ignored setting
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, path, err)
	}
	return nil
}

// applyEnv overrides every string/int field whose JAGA_<PATH> variable is set.
func applyEnv(v reflect.Value, prefix string, lookup func(string) (string, bool)) error {
	t := v.Type()
	for i := range t.NumField() {
		name := prefix + "_" + strings.ToUpper(strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0])
		f := v.Field(i)
		if f.Kind() == reflect.Struct {
			if err := applyEnv(f, name, lookup); err != nil {
				return err
			}
			continue
		}
		s, ok := lookup(name)
		if !ok {
			continue
		}
		switch f.Kind() {
		case reflect.String:
			f.SetString(s)
		case reflect.Int:
			n, err := strconv.Atoi(s)
			if err != nil {
				return fmt.Errorf("%w: %s=%q is not a number", ErrInvalid, name, s)
			}
			f.SetInt(int64(n))
		default:
			panic("config: unsupported field kind " + f.Kind().String())
		}
	}
	return nil
}

// Validate reports the first invalid value.
func (c Config) Validate() error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
	}
	switch {
	case c.Listen == "":
		return bad("listen is empty")
	case c.DataDir == "":
		return bad("data_dir is empty")
	case c.FFmpegPath == "":
		return bad("ffmpeg_path is empty")
	case c.Go2RTC.Mode != "embedded" && c.Go2RTC.Mode != "external":
		return bad("go2rtc.mode is %q, want embedded or external", c.Go2RTC.Mode)
	case c.Storage.DiskQuotaPercent < 1 || c.Storage.DiskQuotaPercent > 99:
		return bad("storage.disk_quota_percent is %d, want 1-99", c.Storage.DiskQuotaPercent)
	case c.Storage.DefaultRetentionDays < 1:
		return bad("storage.default_retention_days is %d, want at least 1", c.Storage.DefaultRetentionDays)
	case strings.HasPrefix(c.SecretKey, "${"):
		// jaga.yml is not env-expanded; set JAGA_SECRET_KEY instead.
		return bad("secret_key is a literal %q; set JAGA_SECRET_KEY instead", c.SecretKey)
	}
	return nil
}
