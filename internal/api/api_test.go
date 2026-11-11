// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/auth"
	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/internal/live"
	"github.com/ryandinulfatah12/jaga/internal/onvif/onviftest"
	"github.com/ryandinulfatah12/jaga/internal/probe"
	"github.com/ryandinulfatah12/jaga/internal/status"
	"github.com/ryandinulfatah12/jaga/migrations"
)

const secret = "0123456789abcdef0123456789abcdef" //nolint:gosec // test key

type fixture struct {
	srv  *httptest.Server
	s    *Server
	logs *strings.Builder
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Open(ctx, filepath.Join(t.TempDir(), "jaga.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn, migrations.FS); err != nil {
		t.Fatal(err)
	}
	seal, err := camera.NewSealer(secret)
	if err != nil {
		t.Fatal(err)
	}
	as := auth.NewStore(conn)
	for _, u := range [][3]string{{"admin", "admin-pass", auth.RoleAdmin}, {"viewer", "viewer-pass", auth.RoleViewer}} {
		if _, err := as.CreateUser(ctx, u[0], u[1], u[2]); err != nil {
			t.Fatal(err)
		}
	}
	logs := &strings.Builder{}
	s := &Server{Auth: as, Cameras: camera.NewStore(conn, seal), Log: slog.New(slog.NewTextHandler(logs, nil)), FFprobe: "ffprobe",
		DB: conn, DataDir: t.TempDir()}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &fixture{srv: srv, s: s, logs: logs}
}

// client is a browser-like client: cookie jar and same-origin fetch headers.
func (f *fixture) client(t *testing.T, user, pass string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if user != "" {
		code, body := f.do(t, c, "POST", "/api/v1/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass))
		if code != 200 {
			t.Fatalf("login %s: %d %s", user, code, body)
		}
	}
	return c
}

func (f *fixture) do(t *testing.T, c *http.Client, method, path, body string, hdr ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), method, f.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestLoginCookieAndLogout(t *testing.T) {
	f := newFixture(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code, _ := f.do(t, c, "GET", "/api/v1/auth/me", ""); code != 401 {
		t.Errorf("me before login = %d", code)
	}
	if code, body := f.do(t, c, "POST", "/api/v1/auth/login", `{"username":"admin","password":"nope-nope"}`); code != 401 || !strings.Contains(body, "wrong username or password") {
		t.Errorf("bad password = %d %s", code, body)
	}

	req, _ := http.NewRequestWithContext(t.Context(), "POST", f.srv.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	ck := resp.Cookies()
	if len(ck) != 1 || ck[0].Name != CookieName || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteLaxMode || ck[0].Secure { // plain HTTP: no Secure
		t.Fatalf("cookie = %+v", ck)
	}
	if code, body := f.do(t, c, "GET", "/api/v1/auth/me", ""); code != 200 || !strings.Contains(body, `"role":"admin"`) {
		t.Errorf("me = %d %s", code, body)
	}
	if code, _ := f.do(t, c, "POST", "/api/v1/auth/logout", ""); code != 204 {
		t.Errorf("logout = %d", code)
	}
	// The old token is dead on the server, not only removed from the jar.
	old := &http.Client{}
	req, _ = http.NewRequestWithContext(t.Context(), "GET", f.srv.URL+"/api/v1/auth/me", nil)
	req.AddCookie(ck[0])
	resp, _ = old.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("old cookie after logout = %d", resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	f := newFixture(t)
	f.s.Limit.Max = 3
	c := f.client(t, "", "")
	for i := range 3 {
		if code, _ := f.do(t, c, "POST", "/api/v1/auth/login", `{"username":"admin","password":"wrong-pass"}`); code != 401 {
			t.Fatalf("attempt %d = %d", i+1, code)
		}
	}
	// Even the right password is refused once the limit is hit.
	req, _ := http.NewRequestWithContext(t.Context(), "POST", f.srv.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"admin-pass"}`))
	resp, _ := c.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Errorf("4th attempt = %d retry-after=%q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if !strings.Contains(f.logs.String(), "login failed") || strings.Contains(f.logs.String(), "wrong-pass") {
		t.Error("failed logins not logged, or password logged")
	}
}

func TestClientIP(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := s.clientIP(r); got != "10.0.0.5" {
		t.Errorf("untrusted proxy: %q", got)
	}
	if s.https(r) {
		t.Error("plain request seen as https")
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if s.https(r) {
		t.Error("X-Forwarded-Proto trusted without TrustProxy")
	}
	s.TrustProxy = true
	if got := s.clientIP(r); got != "10.0.0.5" || s.https(r) {
		t.Errorf("LAN client spoofed its IP or https with TrustProxy: %q %v", got, s.https(r))
	}
	r.RemoteAddr = "127.0.0.1:4321" // cloudflared on the same host
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("trusted proxy: %q", got)
	}
	if !s.https(r) {
		t.Error("X-Forwarded-Proto ignored with TrustProxy")
	}
	w := httptest.NewRecorder()
	s.hsts(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, r)
	if w.Header().Get("Strict-Transport-Security") == "" {
		t.Error("no HSTS over HTTPS")
	}
	r.RemoteAddr = "10.0.0.5:1234"
	w = httptest.NewRecorder()
	s.hsts(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, r)
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS on a plain-HTTP LAN request would lock the LAN install out")
	}
}

func TestCrossOriginWriteRejected(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, "admin", "admin-pass")
	body := `{"name":"cam1","main_url":"rtsp://u:p@10.0.0.2/s"}`
	if code, _ := f.do(t, c, "POST", "/api/v1/cameras", body, "Sec-Fetch-Site", "cross-site"); code != 403 {
		t.Errorf("cross-site POST = %d, want 403", code)
	}
	if code, _ := f.do(t, c, "POST", "/api/v1/cameras", body, "Sec-Fetch-Site", "", "Origin", "https://evil.example"); code != 403 {
		t.Errorf("foreign Origin POST = %d, want 403", code)
	}
	if code, _ := f.do(t, c, "GET", "/api/v1/cameras", "", "Sec-Fetch-Site", "cross-site"); code != 200 {
		t.Errorf("cross-site GET = %d, want 200 (safe method)", code)
	}
}

func TestCameraCRUDMasksURLs(t *testing.T) {
	f := newFixture(t)
	admin := f.client(t, "admin", "admin-pass")
	viewer := f.client(t, "viewer", "viewer-pass")
	anon := f.client(t, "", "")

	create := `{"name":"Gerbang","main_url":"rtsp://admin:hunter2@10.0.0.2:554/main","sub_url":"rtsp://admin:hunter2@10.0.0.2:554/sub","segment_sec":300}`
	if code, _ := f.do(t, viewer, "POST", "/api/v1/cameras", create); code != 403 {
		t.Errorf("viewer create = %d", code)
	}
	if code, _ := f.do(t, anon, "GET", "/api/v1/cameras", ""); code != 401 {
		t.Errorf("anon list = %d", code)
	}
	code, body := f.do(t, admin, "POST", "/api/v1/cameras", create)
	if code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}
	var cam cameraOut
	_ = json.Unmarshal([]byte(body), &cam)
	if cam.MainURL != "rtsp://***@10.0.0.2:554/main" || cam.SegmentSec != 300 || !cam.Enabled {
		t.Errorf("created = %+v", cam)
	}
	id := fmt.Sprint(cam.ID)

	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/v1/cameras", create, 409},
		{"POST", "/api/v1/cameras", `{"name":"x","main_url":"file:///etc/passwd"}`, 422},
		{"POST", "/api/v1/cameras", `{"name":"x"}`, 422},
		{"POST", "/api/v1/cameras", `{"name":"x","main_url":"rtsp://a/s","evil":1}`, 400},
		{"PATCH", "/api/v1/cameras/" + id, `{"retention_days":"7"}`, 422},
		{"PATCH", "/api/v1/cameras/999", `{}`, 404},
		{"GET", "/api/v1/cameras/abc", ``, 404},
		{"GET", "/api/v1/nope", ``, 404},
	} {
		if code, body := f.do(t, admin, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s = %d %s, want %d", c.method, c.path, c.body, code, body, c.want)
		}
	}

	code, body = f.do(t, admin, "PATCH", "/api/v1/cameras/"+id, `{"enabled":false,"retention_days":3}`)
	if code != 200 || !strings.Contains(body, `"enabled":false`) || !strings.Contains(body, `"retention_days":3`) || !strings.Contains(body, `"segment_sec":300`) {
		t.Errorf("patch = %d %s", code, body)
	}
	code, body = f.do(t, admin, "PATCH", "/api/v1/cameras/"+id, `{"retention_days":null}`)
	if code != 200 || !strings.Contains(body, `"retention_days":null`) {
		t.Errorf("patch null retention = %d %s", code, body)
	}
	code, list := f.do(t, viewer, "GET", "/api/v1/cameras", "")
	if code != 200 || !strings.Contains(list, "Gerbang") {
		t.Errorf("viewer list = %d %s", code, list)
	}
	if code, _ := f.do(t, viewer, "DELETE", "/api/v1/cameras/"+id, ""); code != 403 {
		t.Errorf("viewer delete = %d", code)
	}
	if code, _ := f.do(t, admin, "DELETE", "/api/v1/cameras/"+id, ""); code != 204 {
		t.Errorf("delete = %d", code)
	}
	if code, _ := f.do(t, admin, "GET", "/api/v1/cameras/"+id, ""); code != 404 {
		t.Errorf("get after delete = %d", code)
	}
	if strings.Contains(body+list+f.logs.String(), "hunter2") {
		t.Error("camera password leaked in a response or log")
	}
}

func TestCameraTest(t *testing.T) {
	f := newFixture(t)
	admin := f.client(t, "admin", "admin-pass")
	_, body := f.do(t, admin, "POST", "/api/v1/cameras", `{"name":"c","main_url":"rtsp://u:pw@10.0.0.2/main"}`)
	var cam cameraOut
	_ = json.Unmarshal([]byte(body), &cam)
	path := fmt.Sprintf("/api/v1/cameras/%d/test", cam.ID)

	var gotURL string
	f.s.probe = func(_ context.Context, _, url string, _ time.Duration) (probe.Result, error) {
		gotURL = url
		return probe.Result{Streams: []probe.Stream{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}}, Sampled: 5 * time.Second}, nil
	}
	code, body := f.do(t, admin, "POST", path, "")
	if code != 200 || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"codec":"h264"`) || gotURL != "rtsp://u:pw@10.0.0.2/main" {
		t.Errorf("test = %d %s (url %q)", code, body, gotURL)
	}
	if code, _ := f.do(t, admin, "POST", path+"?stream=sub", ""); code != 422 {
		t.Errorf("sub without sub_url = %d", code)
	}

	f.s.probe = func(context.Context, string, string, time.Duration) (probe.Result, error) {
		return probe.Result{}, fmt.Errorf("%w: rtsp://***@10.0.0.2/main: 401 Unauthorized", probe.ErrProbe)
	}
	code, body = f.do(t, admin, "POST", path, "")
	if code != 200 || !strings.Contains(body, `"ok":false`) || strings.Contains(body, "pw@") {
		t.Errorf("unreachable camera = %d %s", code, body)
	}
	viewer := f.client(t, "viewer", "viewer-pass")
	if code, _ := f.do(t, viewer, "POST", path, ""); code != 403 {
		t.Errorf("viewer test = %d", code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	f := newFixture(t)
	req, _ := http.NewRequestWithContext(t.Context(), "GET", f.srv.URL+"/api/v1/auth/me", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
}

func TestLiveAndSnapshot(t *testing.T) {
	var hits []string
	g2r := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.RequestURI())
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, "JPEG")
	}))
	defer g2r.Close()

	f := newFixture(t)
	f.srv.Close()
	f.s.Live = live.NewProxy(strings.TrimPrefix(g2r.URL, "http://"), "")
	changed := 0
	f.s.CamerasChanged = func() { changed++ }
	f.srv = httptest.NewServer(f.s.Handler())
	defer f.srv.Close()

	admin := f.client(t, "admin", "admin-pass")
	anon := f.client(t, "", "")
	_, body := f.do(t, admin, "POST", "/api/v1/cameras", `{"name":"a","main_url":"rtsp://10.0.0.2/main","sub_url":"rtsp://10.0.0.2/sub"}`)
	var cam cameraOut
	_ = json.Unmarshal([]byte(body), &cam)
	f.do(t, admin, "PATCH", fmt.Sprintf("/api/v1/cameras/%d", cam.ID), `{"enabled":true}`)
	if changed != 2 {
		t.Errorf("CamerasChanged called %d times, want 2", changed)
	}
	f.s.Live.SetStreams(map[string]int64{live.StreamName(cam.ID, false): cam.ID, live.StreamName(cam.ID, true): cam.ID})

	snap := fmt.Sprintf("/api/v1/cameras/%d/snapshot", cam.ID)
	if code, _ := f.do(t, anon, "GET", snap, ""); code != 401 {
		t.Errorf("anon snapshot = %d", code)
	}
	if code, body := f.do(t, admin, "GET", snap, ""); code != 200 || body != "JPEG" {
		t.Errorf("snapshot = %d %q", code, body)
	}
	if code, _ := f.do(t, admin, "GET", "/api/v1/cameras/999/snapshot", ""); code != 404 {
		t.Errorf("unknown camera snapshot = %d", code)
	}
	src := live.StreamName(cam.ID, false)
	if code, _ := f.do(t, anon, "GET", "/live/api/frame.jpeg?src="+src, ""); code != 401 {
		t.Errorf("anon live = %d", code)
	}
	if code, _ := f.do(t, admin, "GET", "/live/api/frame.jpeg?src="+src, ""); code != 200 {
		t.Errorf("live frame = %d", code)
	}
	if code, _ := f.do(t, admin, "GET", "/live/api/config", ""); code != 403 {
		t.Errorf("/live/api/config = %d, want 403", code)
	}
	want := []string{"/api/frame.jpeg?src=cam1_sub", "/api/frame.jpeg?src=cam1"}
	if strings.Join(hits, " ") != strings.Join(want, " ") {
		t.Errorf("go2rtc saw %v, want %v", hits, want)
	}
}

func TestStatusAndEventStream(t *testing.T) {
	f := newFixture(t)
	viewer := f.client(t, "viewer", "viewer-pass") // viewers may watch status
	owner := f.client(t, "admin", "admin-pass")
	_, body := f.do(t, owner, "POST", "/api/v1/cameras", `{"name":"a","main_url":"rtsp://u:pw@10.0.0.2/main"}`)
	var cam cameraOut
	_ = json.Unmarshal([]byte(body), &cam)
	path := fmt.Sprintf("/api/v1/cameras/%d/status", cam.ID)

	if code, body := f.do(t, viewer, "GET", path, ""); code != 200 || !strings.Contains(body, `"state":"idle"`) || strings.Contains(body, "since") {
		t.Errorf("status before recording = %d %s", code, body)
	}
	if code, _ := f.do(t, viewer, "GET", "/api/v1/cameras/999/status", ""); code != 404 {
		t.Errorf("unknown camera status = %d", code)
	}
	f.s.Status.Set(status.Status{CameraID: cam.ID, State: status.Recording})
	if _, body := f.do(t, viewer, "GET", path, ""); !strings.Contains(body, `"state":"recording"`) {
		t.Errorf("status = %s", body)
	}
	if code, _ := f.do(t, f.client(t, "", ""), "GET", "/api/v1/events/stream", ""); code != 401 {
		t.Errorf("anon stream = %d", code)
	}

	heartbeat = 50 * time.Millisecond
	t.Cleanup(func() { heartbeat = 25 * time.Second })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.srv.URL+"/api/v1/events/stream", nil)
	resp, err := viewer.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func(prefix string) string {
		t.Helper()
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed waiting for %q", prefix)
				}
				if strings.HasPrefix(l, prefix) {
					return l
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out waiting for %q", prefix)
			}
		}
	}
	next("event: snapshot")
	if l := next("data: "); !strings.Contains(l, `"state":"recording"`) || !strings.Contains(l, fmt.Sprintf(`"camera_id":%d`, cam.ID)) {
		t.Errorf("snapshot = %s", l)
	}
	f.s.Status.Set(status.Status{CameraID: cam.ID, State: status.Retrying, Error: "rtsp://***@10.0.0.2/main: refused"})
	next("event: status")
	if l := next("data: "); !strings.Contains(l, `"state":"retrying"`) {
		t.Errorf("update = %s", l)
	}
	f.s.Status.Set(status.Status{CameraID: cam.ID, State: status.Stopped})
	next("event: status")
	if l := next("data: "); !strings.Contains(l, `"state":"idle"`) {
		t.Errorf("stopped shown as %s, want idle", l)
	}
	next(": ping")
}

func TestPTZ(t *testing.T) {
	f := newFixture(t)
	cam := &onviftest.Camera{User: "u", Pass: "p@ss"}
	onv := httptest.NewServer(cam)
	defer onv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(onv.URL, "http://"))

	admin := f.client(t, "admin", "admin-pass")
	_, body := f.do(t, admin, "POST", "/api/v1/cameras", `{"name":"tapo","main_url":"rtsp://u:p%40ss@`+host+`:554/stream1"}`)
	var c cameraOut
	_ = json.Unmarshal([]byte(body), &c)
	path := fmt.Sprintf("/api/v1/cameras/%d/ptz", c.ID)
	if c.PTZ {
		t.Fatalf("ptz without onvif_port: %s", body)
	}
	if code, body := f.do(t, admin, "POST", path, `{"action":"move","pan":1}`); code != 422 {
		t.Errorf("no onvif_port = %d %s", code, body)
	}
	if code, body := f.do(t, admin, "PATCH", fmt.Sprintf("/api/v1/cameras/%d", c.ID), `{"onvif_port":`+port+`}`); code != 200 || !strings.Contains(body, `"ptz":true`) {
		t.Fatalf("set onvif_port = %d %s", code, body)
	}

	// Viewers may point the camera: it changes no setting.
	viewer := f.client(t, "viewer", "viewer-pass")
	for _, b := range []string{`{"action":"move","pan":0.5,"tilt":-1}`, `{"action":"move","pan":0.5,"tilt":-1}`, `{"action":"stop"}`} {
		if code, body := f.do(t, viewer, "POST", path, b); code != 204 {
			t.Errorf("%s = %d %s", b, code, body)
		}
	}
	want := "ContinuousMove main x=0.500 y=-1.000 PT1S|ContinuousMove main x=0.500 y=-1.000 PT1S|Stop"
	if got := strings.Join(cam.Calls(), "|"); got != want {
		t.Errorf("camera calls = %q, want %q", got, want)
	}
	for _, b := range []string{`{"action":"spin"}`, `{"action":"move","pan":2}`} {
		if code, _ := f.do(t, viewer, "POST", path, b); code != 422 {
			t.Errorf("%s = %d", b, code)
		}
	}

	// Wrong camera password: 502 with the camera's reason, never the URL.
	cam.Pass = "changed"
	code, body := f.do(t, admin, "POST", path, `{"action":"stop"}`)
	if code != 502 || !strings.Contains(body, "Authority failure") || strings.Contains(body, "p@ss") || strings.Contains(body, "p%40ss") {
		t.Errorf("wrong password = %d %s", code, body)
	}
	if code, body := f.do(t, admin, "PATCH", fmt.Sprintf("/api/v1/cameras/%d", c.ID), `{"onvif_port":null}`); code != 200 || !strings.Contains(body, `"ptz":false`) {
		t.Errorf("clear onvif_port = %d %s", code, body)
	}
	if code, _ := f.do(t, admin, "PATCH", fmt.Sprintf("/api/v1/cameras/%d", c.ID), `{"onvif_port":70000}`); code != 422 {
		t.Errorf("bad onvif_port = %d", code)
	}
	if code, _ := f.do(t, &http.Client{}, "POST", path, `{"action":"stop"}`); code != 401 {
		t.Errorf("anonymous = %d", code)
	}
}

func TestSegmentsAndFile(t *testing.T) {
	f := newFixture(t)
	admin := f.client(t, "admin", "admin-pass")
	_, body := f.do(t, admin, "POST", "/api/v1/cameras", `{"name":"c","main_url":"rtsp://10.0.0.2/main"}`)
	var c cameraOut
	_ = json.Unmarshal([]byte(body), &c)

	dir := filepath.Join(f.s.DataDir, "recordings", fmt.Sprint(c.ID), "2026-11-08")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	data := []byte("0123456789abcdef") // stands in for an MP4
	if err := os.WriteFile(filepath.Join(dir, "a.mp4"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	ins := func(path string, start, end int64) int64 {
		res, err := f.s.DB.ExecContext(t.Context(), `INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes) VALUES (?, ?, ?, ?, 16)`, c.ID, path, start, end)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	a := ins(fmt.Sprintf("recordings/%d/2026-11-08/a.mp4", c.ID), 1000, 2000)
	ins(fmt.Sprintf("recordings/%d/2026-11-08/b.mp4", c.ID), 2000, 3000) // file gone
	evil := ins("recordings/../../etc/passwd", 5000, 6000)

	viewer := f.client(t, "viewer", "viewer-pass")
	list := func(q string) (int, []segmentOut) {
		code, body := f.do(t, viewer, "GET", fmt.Sprintf("/api/v1/cameras/%d/segments?%s", c.ID, q), "")
		var out []segmentOut
		_ = json.Unmarshal([]byte(body), &out)
		return code, out
	}
	if code, out := list("from=1500&to=2500"); code != 200 || len(out) != 2 || out[0].ID != a || out[0].Start != 1000 {
		t.Errorf("overlap = %d %+v", code, out)
	}
	if code, out := list("from=2000&to=2001"); code != 200 || len(out) != 1 {
		t.Errorf("end is exclusive: %d %+v", code, out)
	}
	if code, out := list("from=10000&to=20000"); code != 200 || out == nil || len(out) != 0 {
		t.Errorf("empty range must be [] = %d %v", code, out)
	}
	for _, q := range []string{"from=2&to=1", "from=x&to=1", "to=5", fmt.Sprintf("from=0&to=%d", (49 * time.Hour).Milliseconds())} {
		if code, _ := list(q); code != 422 {
			t.Errorf("%s = %d", q, code)
		}
	}
	if code, _ := f.do(t, viewer, "GET", "/api/v1/cameras/999/segments?from=0&to=1", ""); code != 404 {
		t.Errorf("unknown camera = %d", code)
	}

	file := func(id int64, hdr ...string) (int, string, http.Header) {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", fmt.Sprintf("%s/api/v1/segments/%d/file", f.srv.URL, id), nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := viewer.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close() //nolint:errcheck // test
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	if code, body, h := file(a); code != 200 || body != string(data) || h.Get("Content-Type") != "video/mp4" || h.Get("Accept-Ranges") != "bytes" {
		t.Errorf("file = %d %q %v", code, body, h)
	}
	if code, body, h := file(a, "Range", "bytes=4-7"); code != 206 || body != "4567" || h.Get("Content-Range") != "bytes 4-7/16" {
		t.Errorf("range = %d %q %v", code, body, h)
	}
	if code, _, _ := file(a + 1); code != 404 {
		t.Errorf("missing file = %d", code)
	}
	if code, body, _ := file(evil); code != 404 || strings.Contains(body, "root:") {
		t.Errorf("path outside recordings = %d %q", code, body)
	}
	if code, _, _ := file(9999); code != 404 {
		t.Errorf("unknown segment = %d", code)
	}
	if code, _ := f.do(t, &http.Client{}, "GET", fmt.Sprintf("/api/v1/segments/%d/file", a), ""); code != 401 {
		t.Errorf("anonymous = %d", code)
	}
}

func TestSystem(t *testing.T) {
	f := newFixture(t)
	f.s.Tools, f.s.QuotaPercent = []string{"ffmpeg 9 (/x)"}, 90
	admin := f.client(t, "admin", "admin-pass")
	_, body := f.do(t, admin, "POST", "/api/v1/cameras", `{"name":"c","main_url":"rtsp://10.0.0.2/main"}`)
	var c cameraOut
	_ = json.Unmarshal([]byte(body), &c)
	now := time.Now().UnixMilli()
	for _, s := range [][2]int64{{now - 48*3600_000, 100}, {now - 3600_000, 30}, {now - 60_000, 12}} {
		if _, err := f.s.DB.ExecContext(t.Context(), `INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes) VALUES (?, ?, ?, ?, ?)`,
			c.ID, fmt.Sprintf("recordings/%d/x/%d.mp4", c.ID, s[0]), s[0], s[0]+1000, s[1]); err != nil {
			t.Fatal(err)
		}
	}
	viewer := f.client(t, "viewer", "viewer-pass")
	code, body := f.do(t, viewer, "GET", "/api/v1/system", "")
	var out systemOut
	if err := json.Unmarshal([]byte(body), &out); code != 200 || err != nil {
		t.Fatalf("system = %d %s", code, body)
	}
	if len(out.Recordings) != 1 || out.Recordings[0] != (recStats{CameraID: c.ID, Segments: 3, Bytes: 142, Oldest: now - 48*3600_000, Last24h: 42}) {
		t.Errorf("recordings = %+v", out.Recordings)
	}
	if out.Disk.Total == 0 || out.Disk.QuotaPercent != 90 || out.CPUs < 1 || len(out.Tools) != 1 || out.Version == "" {
		t.Errorf("system = %+v", out)
	}
	if code, _ := f.do(t, &http.Client{}, "GET", "/api/v1/system", ""); code != 401 {
		t.Errorf("anonymous = %d", code)
	}
}

func TestClip(t *testing.T) {
	f := newFixture(t)
	admin := f.client(t, "admin", "admin-pass")
	_, body := f.do(t, admin, "POST", "/api/v1/cameras", `{"name":"c","main_url":"rtsp://10.0.0.2/main"}`)
	var c cameraOut
	_ = json.Unmarshal([]byte(body), &c)
	dir := filepath.Join(f.s.DataDir, "recordings", fmt.Sprint(c.ID), "2026-11-08")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.mp4", "c.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []struct {
		path       string
		start, end int64
	}{
		{fmt.Sprintf("recordings/%d/2026-11-08/a.mp4", c.ID), 10_000, 20_000},
		{fmt.Sprintf("recordings/%d/2026-11-08/b.mp4", c.ID), 20_000, 30_000}, // file gone
		{"recordings/../../etc/passwd", 30_000, 40_000},
		{fmt.Sprintf("recordings/%d/2026-11-08/c.mp4", c.ID), 40_000, 50_000},
	} {
		if _, err := f.s.DB.ExecContext(t.Context(), `INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes) VALUES (?, ?, ?, ?, 1)`, c.ID, s.path, s.start, s.end); err != nil {
			t.Fatal(err)
		}
	}

	list, first, err := f.s.concatList(t.Context(), c.ID, 12_500, 45_000)
	if err != nil {
		t.Fatal(err)
	}
	want := "ffconcat version 1.0\nfile 'file:" + filepath.Join(dir, "a.mp4") + "'\ninpoint 2.500\nfile 'file:" + filepath.Join(dir, "c.mp4") + "'\n"
	if list != want || first != 10_000 {
		t.Errorf("list (first %d) =\n%s\nwant\n%s", first, list, want)
	}
	if list, _, _ := f.s.concatList(t.Context(), c.ID, 5_000, 15_000); strings.Contains(list, "inpoint") {
		t.Errorf("range before the first segment needs no inpoint:\n%s", list)
	}

	viewer := f.client(t, "viewer", "viewer-pass")
	clip := func(id int64, q string) int {
		code, _ := f.do(t, viewer, "GET", fmt.Sprintf("/api/v1/cameras/%d/clip?%s", id, q), "")
		return code
	}
	for q, want := range map[string]int{
		"from=2&to=1": 422, "from=x&to=1": 422, "to=5": 422,
		fmt.Sprintf("from=0&to=%d", (2*time.Hour + time.Millisecond).Milliseconds()): 422,
		"from=60000&to=70000": 503, // no FFmpeg configured in the fixture
	} {
		if code := clip(c.ID, q); code != want {
			t.Errorf("%s = %d, want %d", q, code, want)
		}
	}
	f.s.FFmpeg = "ffmpeg"
	if code := clip(c.ID, "from=60000&to=70000"); code != 404 {
		t.Errorf("empty range = %d, want 404", code)
	}
	if code := clip(999, "from=0&to=1"); code != 404 {
		t.Errorf("unknown camera = %d", code)
	}
	if code, _ := f.do(t, &http.Client{}, "GET", fmt.Sprintf("/api/v1/cameras/%d/clip?from=0&to=1", c.ID), ""); code != 401 {
		t.Errorf("anonymous = %d", code)
	}
}
