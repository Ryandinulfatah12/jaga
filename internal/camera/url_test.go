// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package camera

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateURLAccepts(t *testing.T) {
	for _, u := range []string{
		"rtsp://192.168.0.60:554/stream1",
		"rtsp://admin:s3cret@192.168.0.60/stream2",
		"rtsps://cam.local:322/live?channel=1&subtype=0",
		"rtsp://user:p%40ss@[fe80::1]:554/h264",      // encoded '@' in password, IPv6 host
		"rtsp://cam.local/cam/realmonitor?channel=1", // Dahua style
		"rtsp://admin:my%20pass@cam/stream",          // encoded space in password
	} {
		if err := ValidateURL(u); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
}

func TestValidateURLRejects(t *testing.T) {
	for name, u := range map[string]string{
		"empty":               "",
		"file":                "file:///etc/passwd",
		"concat":              "concat:/data/a.mp4|/etc/passwd",
		"exec (go2rtc)":       "exec:ffmpeg -i /dev/video0",
		"ffmpeg (go2rtc)":     "ffmpeg:rtsp://cam/stream#video=h264",
		"http":                "http://cam/stream.mjpeg",
		"option injection":    "-i /etc/passwd",
		"option after scheme": "-rtsp://cam/stream",
		"uppercase scheme":    "RTSP://cam/stream",
		"no slashes":          "rtsp:cam/stream",
		"newline":             "rtsp://cam/stream\n-f null",
		"carriage return":     "rtsp://cam/stream\r",
		"space":               "rtsp://cam/stream -f null",
		"tab":                 "rtsp://cam/\tstream",
		"encoded newline":     "rtsp://cam/stream%0d%0aX-Injected:%201",
		"encoded in password": "rtsp://user:pa%0Ass@cam/stream",
		"encoded tab":         "rtsp://cam/stream%09",
		"bad encoding":        "rtsp://cam/stream%zz",
		"go2rtc options":      "rtsp://cam/stream#backchannel=1",
		"nul byte":            "rtsp://cam/stream\x00",
		"invalid utf-8":       "rtsp://cam/\xff",
		"missing host":        "rtsp:///stream",
		"only userinfo":       "rtsp://user:pass@/stream",
		"port zero":           "rtsp://cam:0/stream",
		"port too big":        "rtsp://cam:70000/stream",
		"port not a number":   "rtsp://cam:abc/stream",
		"too long":            "rtsp://cam/" + strings.Repeat("a", maxURLLen),
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateURL(u)
			if !errors.Is(err, ErrInvalidURL) {
				t.Fatalf("err = %v, want ErrInvalidURL", err)
			}
		})
	}
}

func TestValidateURLErrorHidesCredentials(t *testing.T) {
	for _, u := range []string{
		"rtsp://admin:hunter2@cam:99999/stream",
		"rtsp://admin:hunter2@cam/st ream",
		"http://admin:hunter2@cam/stream",
	} {
		err := ValidateURL(u)
		if err == nil {
			t.Fatalf("%q accepted", u)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("error leaks the password: %v", err)
		}
	}
}

// FuzzValidateURL checks that anything accepted keeps the properties FFmpeg
// and go2rtc rely on. `go test` runs the seeds; `go test -fuzz=.` explores.
func FuzzValidateURL(f *testing.F) {
	for _, s := range []string{"rtsp://cam/s", "rtsps://u:p@cam:554/s?a=1", "-i x", "rtsp://c/%0a", "exec:id"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateURL(s) != nil {
			return
		}
		if !strings.HasPrefix(s, "rtsp://") && !strings.HasPrefix(s, "rtsps://") {
			t.Fatalf("accepted non-rtsp URL %q", s)
		}
		if strings.ContainsAny(s, "# \t\r\n\x00") {
			t.Fatalf("accepted unsafe characters in %q", s)
		}
	})
}
