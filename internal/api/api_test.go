// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/auth"
	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/internal/probe"
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
	s := &Server{Auth: as, Cameras: camera.NewStore(conn, seal), Log: slog.New(slog.NewTextHandler(logs, nil)), FFprobe: "ffprobe"}
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
	if got := s.clientIP(r); got != "203.0.113.9" {
		t.Errorf("trusted proxy: %q", got)
	}
	if !s.https(r) {
		t.Error("X-Forwarded-Proto ignored with TrustProxy")
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
