// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package live

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ryandinulfatah12/jaga/internal/camera"
)

func cam(id int64, main, sub string, enabled bool) camera.Camera {
	c := camera.New("c", main)
	c.ID, c.SubURL, c.Enabled = id, sub, enabled
	return c
}

func TestBuildConfig(t *testing.T) {
	cfg, err := BuildConfig("127.0.0.1:1984", 8555, []camera.Camera{
		cam(7, "rtsp://admin:hunter2@10.0.0.7/main", "rtsp://admin:hunter2@10.0.0.7/sub", true),
		cam(2, "rtsp://10.0.0.2/main", "", true),
		cam(3, "rtsp://10.0.0.3/main", "", false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg.YAML), "hunter2") || strings.Contains(string(cfg.YAML), "rtsp://") {
		t.Errorf("URL written to the config file:\n%s", cfg.YAML)
	}
	var y yamlConfig
	if err := yaml.Unmarshal(cfg.YAML, &y); err != nil {
		t.Fatal(err)
	}
	if y.API.Listen != "127.0.0.1:1984" || y.RTSP.Listen != "" || y.WebRTC.Listen != ":8555" {
		t.Errorf("listeners = %+v", y)
	}
	// Sorted by id: cam2 is JAGA_CAM_1. The disabled camera has no stream.
	want := map[string]string{"cam2": "${JAGA_CAM_1}", "cam7": "${JAGA_CAM_2}", "cam7_sub": "${JAGA_CAM_3}"}
	if len(y.Streams) != len(want) {
		t.Errorf("streams = %v", y.Streams)
	}
	for k, v := range want {
		if y.Streams[k] != v {
			t.Errorf("stream %s = %q, want %q", k, y.Streams[k], v)
		}
	}
	if strings.Join(cfg.Env, " ") != "JAGA_CAM_1=rtsp://10.0.0.2/main JAGA_CAM_2=rtsp://admin:hunter2@10.0.0.7/main JAGA_CAM_3=rtsp://admin:hunter2@10.0.0.7/sub" {
		t.Errorf("env = %v", cfg.Env)
	}
	if cfg.Streams["cam7_sub"] != 7 || len(cfg.Streams) != 3 {
		t.Errorf("stream ids = %v", cfg.Streams)
	}

	dir := t.TempDir()
	path, err := WriteConfig(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("config file mode = %v %v", fi.Mode().Perm(), err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".go2rtc-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestBuildConfigRejectsBadURL(t *testing.T) {
	// The store validates too; the go2rtc config is checked on its own anyway.
	for _, u := range []string{"exec:rm -rf /", "ffmpeg:rtsp://x/s#video=copy", "rtsp://x/s\nstreams:"} {
		if _, err := BuildConfig("127.0.0.1:1984", 8555, []camera.Camera{cam(1, u, "", true)}); err == nil {
			t.Errorf("accepted %q", u)
		}
	}
	if _, err := BuildConfig("1984", 8555, nil); err == nil {
		t.Error("accepted api without host")
	}
}

func TestProxyWhitelist(t *testing.T) {
	var got []string
	g2r := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI()+" origin="+r.Header.Get("Origin")+" cookie="+r.Header.Get("Cookie"))
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.URL.Query().Get("src") == "down" {
			w.Header().Set("Content-Length", "0") // what go2rtc does when the camera fails
			return
		}
		_, _ = io.WriteString(w, "jpeg")
	}))
	defer g2r.Close()
	p := NewProxy(strings.TrimPrefix(g2r.URL, "http://"))
	p.SetStreams(map[string]int64{"cam1": 1, "cam1_sub": 1})
	srv := httptest.NewServer(p)
	defer srv.Close()

	do := func(method, path string, hdr ...string) (int, http.Header) {
		req, _ := http.NewRequestWithContext(t.Context(), method, srv.URL+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode, resp.Header
	}
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/live/api/config", 403},
		{"POST", "/live/api/config", 403},
		{"PUT", "/live/api/streams?src=cam1&name=x", 403},
		{"GET", "/live/api/streams", 403},
		{"GET", "/live/api", 403},
		{"GET", "/live/api/exit", 403},
		{"GET", "/live/stream.html", 403},
		{"GET", "/live/api/frame.jpeg/../config", 403},
		{"GET", "/live/api/frame.jpeg?src=cam9", 404},
		{"GET", "/live/api/frame.jpeg?src=" + url.QueryEscape("exec:id"), 404},
		{"GET", "/live/api/frame.jpeg", 404},
		{"POST", "/live/api/frame.jpeg?src=cam1", 405},
		{"GET", "/live/api/webrtc?src=cam1", 405},
	} {
		if code, _ := do(c.method, c.path); code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, code, c.want)
		}
	}
	if len(got) != 0 {
		t.Fatalf("refused requests reached go2rtc: %v", got)
	}

	// Allowed: extra query params are dropped, Cookie and Origin are not forwarded.
	code, h := do("GET", "/live/api/frame.jpeg?src=cam1_sub&dst=exec:id", "Cookie", "jaga_session=secret", "Origin", srv.URL)
	if code != 200 || h.Get("Access-Control-Allow-Origin") != "" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("allowed snapshot = %d %v", code, h)
	}
	if code, _ := do("POST", "/live/api/webrtc?src=cam1"); code != 200 {
		t.Errorf("webrtc = %d", code)
	}
	if len(got) != 2 || got[0] != "GET /api/frame.jpeg?src=cam1_sub origin= cookie=" || got[1] != "POST /api/webrtc?src=cam1 origin= cookie=" {
		t.Errorf("forwarded = %q", got)
	}

	// Cross-origin: refused before go2rtc, also for GET (WebSocket upgrades).
	for _, hdr := range [][]string{{"Sec-Fetch-Site", "cross-site"}, {"Origin", "https://evil.example"}} {
		if code, _ := do("GET", "/live/api/ws?src=cam1", hdr...); code != 403 {
			t.Errorf("cross-origin %v = %d", hdr, code)
		}
	}
	if len(got) != 2 {
		t.Errorf("cross-origin reached go2rtc: %v", got)
	}

	p.SetStreams(map[string]int64{"down": 3})
	if code, _ := do("GET", "/live/api/frame.jpeg?src=down"); code != 502 {
		t.Errorf("empty frame = %d, want 502", code)
	}
	got = got[:2]

	// Streams are replaced on reload.
	p.SetStreams(map[string]int64{"cam2": 2})
	if code, _ := do("GET", "/live/api/frame.jpeg?src=cam1"); code != 404 {
		t.Errorf("removed stream = %d", code)
	}
	g2r.Close()
	if code, _ := do("GET", "/live/api/frame.jpeg?src=cam2"); code != 502 {
		t.Errorf("go2rtc down = %d", code)
	}
}
