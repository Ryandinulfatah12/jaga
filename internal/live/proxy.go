// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package live

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
)

var errCameraDown = errors.New("camera unreachable")

// allowed are the go2rtc endpoints the browser may reach, by method.
// Everything else (config, stream editing, exec, ...) is refused.
var allowed = map[string]string{
	"/api/ws":         http.MethodGet,  // MSE and WebRTC signalling over WebSocket
	"/api/webrtc":     http.MethodPost, // WebRTC offer/answer over HTTP
	"/api/frame.jpeg": http.MethodGet,  // snapshot
}

// Proxy forwards /live/<allowed endpoint>?src=<stream> to go2rtc. Callers
// must check the session first; the API wraps it with requireUser.
type Proxy struct {
	rp      *httputil.ReverseProxy
	streams atomic.Pointer[map[string]int64]
}

// NewProxy proxies to the go2rtc API at addr (host:port).
func NewProxy(addr string) *Proxy {
	target := &url.URL{Scheme: "http", Host: addr}
	p := &Proxy{}
	p.SetStreams(nil)
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, "/live")
			// Rebuild the query: only src, and only a known stream name, so
			// go2rtc never sees a URL it could turn into a new source.
			r.Out.URL.RawQuery = url.Values{"src": {r.In.URL.Query().Get("src")}}.Encode()
			// Origin was checked in ServeHTTP. go2rtc compares it with its own
			// host, which is loopback, so drop it.
			r.Out.Header.Del("Origin")
			r.Out.Header.Del("Cookie") // the Jaga session is none of go2rtc's business
		},
		ModifyResponse: func(resp *http.Response) error {
			// go2rtc answers 200 with an empty body when the camera cannot be
			// reached; make that an error the frontend can show.
			if resp.Request.URL.Path == "/api/frame.jpeg" && resp.StatusCode == http.StatusOK && resp.ContentLength == 0 {
				return errCameraDown
			}
			resp.Header.Del("Access-Control-Allow-Origin") // go2rtc allows any origin
			resp.Header.Del("Access-Control-Allow-Methods")
			resp.Header.Del("Access-Control-Allow-Headers")
			resp.Header.Set("Cache-Control", "no-store")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, errCameraDown) {
				http.Error(w, "camera unreachable", http.StatusBadGateway)
				return
			}
			http.Error(w, "live view unavailable", http.StatusBadGateway)
		},
	}
	return p
}

// SetStreams replaces the stream names that may be requested (from Config.Streams).
func (p *Proxy) SetStreams(s map[string]int64) {
	if s == nil {
		s = map[string]int64{}
	}
	p.streams.Store(&s)
}

// HasStream reports whether name is a current go2rtc stream.
func (p *Proxy) HasStream(name string) bool {
	_, ok := (*p.streams.Load())[name]
	return ok
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/live")
	method, ok := allowed[path]
	switch {
	case !ok:
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case r.Method != method:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	case !p.HasStream(r.URL.Query().Get("src")):
		http.Error(w, "unknown stream", http.StatusNotFound)
		return
	case !sameOrigin(r):
		// WebSocket upgrades are GETs, so CrossOriginProtection lets them
		// through; check here so another site cannot open a live stream with
		// the user's cookie.
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	p.rp.ServeHTTP(w, r)
}

// sameOrigin accepts requests without an Origin (non-browser clients) or
// whose Origin host equals the Host header.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
