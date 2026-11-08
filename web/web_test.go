// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	h := Handler()
	get := func(method, path string) (int, string, http.Header) {
		r := httptest.NewRequestWithContext(t.Context(), method, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		b, _ := io.ReadAll(w.Result().Body)
		return w.Code, string(b), w.Result().Header
	}
	// index.html for / and for unknown paths (client routes), never cached.
	for _, p := range []string{"/", "/index.html", "/some/client/route", "/../../etc/passwd"} {
		code, body, hdr := get("GET", p)
		if code != 200 || !strings.Contains(body, "<html") || hdr.Get("Cache-Control") != "no-cache" {
			t.Errorf("GET %s = %d %q cache=%q", p, code, body[:min(len(body), 40)], hdr.Get("Cache-Control"))
		}
	}
	if code, _, _ := get("POST", "/"); code != 405 {
		t.Errorf("POST / = %d", code)
	}
	if code, body, _ := get("HEAD", "/"); code != 200 || body != "" {
		t.Errorf("HEAD / = %d %q", code, body)
	}
}
