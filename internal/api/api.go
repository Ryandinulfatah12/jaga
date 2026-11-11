// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package api serves the REST API under /api/v1 with net/http routing.
// Every endpoint except login needs a session cookie; requests that change
// data are also checked for cross-origin forgery (http.CrossOriginProtection).
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/auth"
	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/live"
	"github.com/ryandinulfatah12/jaga/internal/probe"
	"github.com/ryandinulfatah12/jaga/internal/status"
)

// CookieName holds the session token.
const CookieName = "jaga_session"

// Server holds what the handlers need. Build it, then serve Handler().
type Server struct {
	Auth    *auth.Store
	Cameras *camera.Store
	Log     *slog.Logger
	// FFprobe is the ffprobe binary for POST /cameras/{id}/test.
	FFprobe string
	// FFmpeg joins segments for clip export; "" disables it.
	FFmpeg string
	// TrustProxy reads the client IP (rate limit) from X-Forwarded-For and
	// HTTPS from X-Forwarded-Proto (config trust_proxy). Only enable behind a
	// proxy that sets them; otherwise every proxied client shares one
	// rate-limit bucket and the cookie lacks Secure.
	TrustProxy bool

	// Live proxies /live/* to go2rtc; nil disables live view and snapshots.
	Live *live.Proxy
	// CamerasChanged is called after a camera is created, changed or
	// deleted, so recording and live view pick up the change. Optional.
	CamerasChanged func()
	// Status is the recorder state of every camera; nil means an empty hub.
	Status *status.Hub
	// UI serves the web UI at /; nil serves only the API.
	UI http.Handler
	// DB and DataDir serve recordings (timeline and segment files).
	DB      *sql.DB
	DataDir string
	// For GET /system: tool versions, start time and the disk quota.
	Tools        []string
	Started      time.Time
	QuotaPercent int

	// Limit is the login rate limit; nil means 5 per minute per IP.
	Limit *auth.Limiter
	ptzs  ptzCache
	// probe is replaceable in tests.
	probe func(ctx context.Context, ffprobe, url string, sample time.Duration) (probe.Result, error)
}

type ctxKey struct{}

// Handler returns the API with all middleware applied.
func (s *Server) Handler() http.Handler {
	if s.Limit == nil {
		s.Limit = auth.NewLimiter(5, time.Minute)
	}
	if s.probe == nil {
		s.probe = probe.Probe
	}
	if s.Status == nil {
		s.Status = status.NewHub()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)

	user := func(h http.HandlerFunc) http.Handler { return s.requireUser(h, false) }
	admin := func(h http.HandlerFunc) http.Handler { return s.requireUser(h, true) }
	mux.Handle("GET /api/v1/auth/me", user(s.me))
	mux.Handle("GET /api/v1/cameras", user(s.listCameras))
	mux.Handle("POST /api/v1/cameras", admin(s.createCamera))
	mux.Handle("GET /api/v1/cameras/{id}", user(s.getCamera))
	mux.Handle("PATCH /api/v1/cameras/{id}", admin(s.patchCamera))
	mux.Handle("DELETE /api/v1/cameras/{id}", admin(s.deleteCamera))
	mux.Handle("POST /api/v1/cameras/{id}/test", admin(s.testCamera))
	mux.Handle("GET /api/v1/cameras/{id}/snapshot", user(s.snapshot))
	mux.Handle("GET /api/v1/cameras/{id}/status", user(s.cameraStatus))
	mux.Handle("POST /api/v1/cameras/{id}/ptz", user(s.ptz))
	mux.Handle("GET /api/v1/cameras/{id}/segments", user(s.segments))
	mux.Handle("GET /api/v1/segments/{id}/file", user(s.segmentFile))
	mux.Handle("GET /api/v1/cameras/{id}/clip", user(s.clip))
	mux.Handle("GET /api/v1/events/stream", user(s.eventStream))
	mux.Handle("GET /api/v1/system", user(s.system))
	if s.Live != nil {
		mux.Handle("/live/", user(s.Live.ServeHTTP))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	if s.UI != nil {
		mux.Handle("/", s.UI) // the login page is part of the UI, so no session check here
	}

	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
	}))
	return securityHeaders(s.hsts(csrf.Handler(mux)))
}

// securityHeaders applies to every response, API and (later) the frontend.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// No inline script or style, no CDN: everything comes from the binary.
		// blob: for the MSE player; ws/wss are covered by 'self' in current
		// browsers (CSP3).
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; media-src 'self' blob:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// hsts tells browsers to stay on HTTPS once they reached Jaga over it.
// Plain-HTTP LAN installs never get it, so they keep working.
func (s *Server) hsts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.https(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireUser(next http.HandlerFunc, adminOnly bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		u, err := s.Auth.Session(r.Context(), c.Value)
		if errors.Is(err, auth.ErrNoSession) {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if adminOnly && u.Role != auth.RoleAdmin {
			writeError(w, http.StatusForbidden, "admin only")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

func currentUser(r *http.Request) auth.User {
	u, _ := r.Context().Value(ctxKey{}).(auth.User)
	return u
}

// https reports whether the client reached us over TLS, so the cookie gets Secure.
func (s *Server) https(r *http.Request) bool {
	return r.TLS != nil || s.viaProxy(r) && r.Header.Get("X-Forwarded-Proto") == "https"
}

// viaProxy reports whether the forwarded headers can be believed: TrustProxy
// is on and the request comes from loopback, where the proxy or tunnel
// (cloudflared, Caddy) runs. A LAN client talking to the port directly
// cannot spoof its IP this way.
// ponytail: a proxy on another host needs a trusted_proxies list.
func (s *Server) viaProxy(r *http.Request) bool {
	if !s.TrustProxy {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

// clientIP is the rate-limit key.
func (s *Server) clientIP(r *http.Request) string {
	if s.viaProxy(r) {
		// The last hop is the one our proxy appended; earlier ones are client-controlled.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if ok, wait := s.Limit.Allow(s.clientIP(r)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}
	var in struct{ Username, Password string }
	if !decode(w, r, &in) {
		return
	}
	u, token, err := s.Auth.Login(r.Context(), in.Username, in.Password)
	if errors.Is(err, auth.ErrBadLogin) {
		s.Log.Warn("login failed", "user", in.Username, "ip", s.clientIP(r))
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	// Secure follows the request scheme: a plain-HTTP LAN install must still
	// be able to log in.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is set per request by s.https
		Name: CookieName, Value: token, Path: "/",
		MaxAge: int(auth.SessionTTL.Seconds()), HttpOnly: true, Secure: s.https(r), SameSite: http.SameSiteLaxMode,
	})
	s.Log.Info("login", "user", u.Username, "ip", s.clientIP(r))
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := s.Auth.Logout(r.Context(), c.Value); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.https(r), SameSite: http.SameSiteLaxMode}) //nolint:gosec // G124: see login
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

// internal logs the real error and returns a generic 500, so SQL or file
// details never reach the client.
func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decode reads a JSON body of at most 64 KiB, rejecting unknown fields.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}
