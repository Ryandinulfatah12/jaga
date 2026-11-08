// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/onvif"
)

// ptzTimeout bounds every move: the camera stops by itself even when the
// browser never sends stop (tab closed, network gone). The UI repeats the
// move while the button is held.
const ptzTimeout = time.Second

type ptzIn struct {
	Action string  `json:"action"` // "move" or "stop"
	Pan    float64 `json:"pan"`    // -1 (left) .. 1 (right)
	Tilt   float64 `json:"tilt"`   // -1 (down) .. 1 (up)
	Zoom   float64 `json:"zoom"`   // -1 (out) .. 1 (in); cameras without zoom ignore it
}

// ptzCache keeps the discovered PTZ service per camera, so a held button
// is one SOAP call per repeat. Keyed by camera id and the host:port it was
// found on, so editing the camera finds it again.
type ptzCache struct {
	mu sync.Mutex
	m  map[int64]ptzEntry
}

type ptzEntry struct {
	host string
	ptz  onvif.PTZ
}

// ptz moves or stops a camera through ONVIF. Admin and viewer both may
// use it: it only points the camera, it changes no setting or recording.
// ponytail: no per-user PTZ lock; add one if two people fight over a camera.
func (s *Server) ptz(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in ptzIn
	if !decode(w, r, &in) {
		return
	}
	switch {
	case in.Action != "move" && in.Action != "stop":
		writeError(w, http.StatusUnprocessableEntity, `action must be "move" or "stop"`)
		return
	case in.Pan < -1 || in.Pan > 1 || in.Tilt < -1 || in.Tilt > 1 || in.Zoom < -1 || in.Zoom > 1:
		writeError(w, http.StatusUnprocessableEntity, "pan, tilt and zoom must be between -1 and 1")
		return
	}
	c, err := s.Cameras.Get(r.Context(), id)
	if err != nil {
		s.cameraError(w, r, err)
		return
	}
	client, err := onvifClient(c)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	p, err := s.ptzService(ctx, c.ID, client)
	if err == nil {
		if in.Action == "stop" {
			err = client.Stop(ctx, p)
		} else {
			err = client.Move(ctx, p, in.Pan, in.Tilt, in.Zoom, ptzTimeout)
		}
	}
	if errors.Is(err, onvif.ErrONVIF) {
		s.forgetPTZ(c.ID) // the camera may have changed; discover again next time
		writeError(w, http.StatusBadGateway, camera.MaskText(err.Error()))
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// onvifClient builds the client from main_url: same host, same account.
func onvifClient(c camera.Camera) (*onvif.Client, error) {
	if c.ONVIFPort == nil {
		return nil, errors.New("camera has no onvif_port")
	}
	u, err := url.Parse(c.MainURL)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("main_url has no host") // never echo the URL: it holds the password
	}
	pass, _ := u.User.Password()
	return &onvif.Client{Host: net.JoinHostPort(u.Hostname(), strconv.Itoa(*c.ONVIFPort)), User: u.User.Username(), Pass: pass}, nil
}

func (s *Server) ptzService(ctx context.Context, id int64, c *onvif.Client) (onvif.PTZ, error) {
	s.ptzs.mu.Lock()
	e, ok := s.ptzs.m[id]
	s.ptzs.mu.Unlock()
	if ok && e.host == c.Host {
		return e.ptz, nil
	}
	p, err := c.DiscoverPTZ(ctx)
	if err != nil {
		return onvif.PTZ{}, err
	}
	s.ptzs.mu.Lock()
	if s.ptzs.m == nil {
		s.ptzs.m = map[int64]ptzEntry{}
	}
	s.ptzs.m[id] = ptzEntry{host: c.Host, ptz: p}
	s.ptzs.mu.Unlock()
	return p, nil
}

func (s *Server) forgetPTZ(id int64) {
	s.ptzs.mu.Lock()
	delete(s.ptzs.m, id)
	s.ptzs.mu.Unlock()
}
