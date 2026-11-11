// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package live runs go2rtc for live view and proxies the browser to it.
//
// go2rtc's API can create exec: sources that run any command, so it is
// treated as hostile surface: it listens on loopback only, its RTSP server
// is off, and the browser reaches it only through Proxy, which allows a
// handful of read-only endpoints for streams of known cameras.
package live

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/ryandinulfatah12/jaga/internal/camera"
)

// StreamName is the go2rtc stream for a camera: "cam<ID>" for the main
// stream, "cam<ID>_sub" for the sub stream. IDs are stable and never reused
// for a different camera name, so renames do not touch go2rtc.
func StreamName(id int64, sub bool) string {
	if sub {
		return "cam" + strconv.FormatInt(id, 10) + "_sub"
	}
	return "cam" + strconv.FormatInt(id, 10)
}

type yamlConfig struct {
	API struct {
		Listen    string `yaml:"listen"`
		Username  string `yaml:"username,omitempty"`
		Password  string `yaml:"password,omitempty"`
		LocalAuth bool   `yaml:"local_auth,omitempty"`
	} `yaml:"api"`
	RTSP struct {
		Listen string `yaml:"listen"`
	} `yaml:"rtsp"`
	WebRTC struct {
		Listen string `yaml:"listen"`
	} `yaml:"webrtc"`
	Log struct {
		Level string `yaml:"level"`
	} `yaml:"log"`
	Streams map[string]string `yaml:"streams"`
}

// Config is a go2rtc config plus the environment it expects. Camera URLs
// are never written to the file: each stream is "${JAGA_CAM_<n>}", expanded
// by go2rtc from Env, so credentials stay out of data_dir and the URL can
// never be parsed as a go2rtc source prefix like exec: or ffmpeg:.
type Config struct {
	YAML []byte
	Env  []string // KEY=value, URLs with credentials
	// Streams maps each stream name to the camera it belongs to.
	Streams map[string]int64
}

// APIUser is the go2rtc API username; the password is per Jaga process
// (NewAPIPassword) and reaches go2rtc only through its environment.
const APIUser = "jaga"

// NewAPIPassword returns a random go2rtc API password.
func NewAPIPassword() string { return rand.Text() }

// BuildConfig returns the go2rtc config for the enabled cameras. Disabled
// cameras get no stream. URLs are validated again here: the config is a
// trust boundary even though the store already validated them.
//
// apiPass locks the go2rtc API, loopback included (local_auth): without it
// any local process could POST /api/config with an exec: source and run
// commands as jaga, or read the camera URLs.
func BuildConfig(api string, webrtcPort int, apiPass string, cams []camera.Camera) (Config, error) {
	if _, _, err := net.SplitHostPort(api); err != nil {
		return Config{}, fmt.Errorf("go2rtc api %q: %w", api, err)
	}
	if apiPass == "" {
		return Config{}, fmt.Errorf("go2rtc api password is empty")
	}
	var y yamlConfig
	y.API.Listen = api
	y.API.Username, y.API.Password, y.API.LocalAuth = APIUser, "${JAGA_G2R_PASS}", true
	y.RTSP.Listen = "" // RTSP server off: it would re-publish every camera without auth
	y.WebRTC.Listen = ":" + strconv.Itoa(webrtcPort)
	y.Log.Level = "warn"
	y.Streams = map[string]string{}

	out := Config{Streams: map[string]int64{}, Env: []string{"JAGA_G2R_PASS=" + apiPass}}
	sorted := append([]camera.Camera(nil), cams...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	n := 0
	add := func(name, url string, id int64) error {
		if err := camera.ValidateURL(url); err != nil {
			return fmt.Errorf("camera %d: %w", id, err)
		}
		n++
		key := "JAGA_CAM_" + strconv.Itoa(n)
		y.Streams[name] = "${" + key + "}"
		out.Env = append(out.Env, key+"="+url)
		out.Streams[name] = id
		return nil
	}
	for _, c := range sorted {
		if !c.Enabled {
			continue
		}
		if err := add(StreamName(c.ID, false), c.MainURL, c.ID); err != nil {
			return Config{}, err
		}
		if c.SubURL != "" {
			if err := add(StreamName(c.ID, true), c.SubURL, c.ID); err != nil {
				return Config{}, err
			}
		}
	}
	b, err := yaml.Marshal(y)
	if err != nil {
		return Config{}, err
	}
	out.YAML = b
	return out, nil
}

// WriteConfig writes the config to dir/go2rtc.yaml atomically, readable by
// the owner only.
func WriteConfig(dir string, cfg Config) (string, error) {
	path := filepath.Join(dir, "go2rtc.yaml")
	tmp, err := os.CreateTemp(dir, ".go2rtc-*.yaml")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after the rename
	if _, err := tmp.Write(cfg.YAML); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}
