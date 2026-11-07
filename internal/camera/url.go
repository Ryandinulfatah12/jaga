// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package camera holds the camera model and its validation.
package camera

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidURL wraps every rejected camera URL, so the API can answer 400.
// Messages never repeat the URL: it usually carries the camera password.
var ErrInvalidURL = errors.New("invalid camera URL")

// maxURLLen is far above any real camera URL and keeps abuse out of logs and argv.
const maxURLLen = 2048

// ValidateURL accepts only rtsp:// and rtsps:// URLs that are safe to pass to
// FFmpeg as a single argv entry and to go2rtc as a source.
//
// It rejects, among others: other schemes (file:, concat:, exec:, ffmpeg:),
// values that FFmpeg would read as an option (-i ...), whitespace and control
// characters (control characters also when percent-encoded, since they could
// smuggle headers into the RTSP request), and '#', which go2rtc reads as source
// options.
func ValidateURL(raw string) error {
	bad := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidURL, reason) }

	switch {
	case raw == "":
		return bad("empty")
	case len(raw) > maxURLLen:
		return bad("longer than " + strconv.Itoa(maxURLLen) + " bytes")
	case strings.HasPrefix(raw, "-"):
		return bad("starts with '-'")
	case !strings.HasPrefix(raw, "rtsp://") && !strings.HasPrefix(raw, "rtsps://"):
		// Checked on the raw string, lowercase only: FFmpeg matches protocol names exactly.
		return bad("scheme must be rtsp:// or rtsps://")
	case strings.ContainsRune(raw, '#'):
		return bad("contains '#'")
	case hasSpaceOrControl(raw):
		return bad("contains whitespace or control characters")
	}

	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return bad("malformed percent-encoding")
	}
	// Encoded spaces are fine (FFmpeg sends the URL still encoded, and camera
	// passwords may contain them), but encoded CR/LF and other control bytes
	// could end up as raw bytes in the RTSP request or the auth header.
	if strings.IndexFunc(decoded, func(r rune) bool { return unicode.IsControl(r) || r == utf8.RuneError }) >= 0 {
		return bad("contains encoded control characters")
	}

	u, err := url.Parse(raw)
	if err != nil {
		// url.Error quotes the input, so only report that parsing failed.
		return bad("cannot be parsed")
	}
	if u.Hostname() == "" {
		return bad("missing host")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return bad("port out of range")
		}
	}
	return nil
}

func hasSpaceOrControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		// utf8.RuneError also covers invalid UTF-8, which has no place in a URL.
		return unicode.IsSpace(r) || unicode.IsControl(r) || r == utf8.RuneError
	}) >= 0
}
