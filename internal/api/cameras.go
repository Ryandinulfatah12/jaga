// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
	"github.com/ryandinulfatah12/jaga/internal/probe"
)

// cameraOut is a camera as the API shows it: URLs always masked.
type cameraOut struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	MainURL       string          `json:"main_url"`
	SubURL        string          `json:"sub_url,omitempty"`
	Enabled       bool            `json:"enabled"`
	RecordMode    string          `json:"record_mode"`
	RecordAudio   bool            `json:"record_audio"`
	SegmentSec    int             `json:"segment_sec"`
	RetentionDays *int            `json:"retention_days"`
	MotionMode    string          `json:"motion_mode"`
	MotionConfig  json.RawMessage `json:"motion_config"`
	CreatedAt     time.Time       `json:"created_at"`
}

func out(c camera.Camera) cameraOut {
	o := cameraOut{
		ID: c.ID, Name: c.Name, MainURL: camera.MaskURL(c.MainURL), Enabled: c.Enabled,
		RecordMode: c.RecordMode, RecordAudio: c.RecordAudio, SegmentSec: c.SegmentSec,
		RetentionDays: c.RetentionDays, MotionMode: c.MotionMode, MotionConfig: c.MotionConfig, CreatedAt: c.CreatedAt,
	}
	if c.SubURL != "" {
		o.SubURL = camera.MaskURL(c.SubURL)
	}
	return o
}

// cameraIn is a create or PATCH body. Pointers tell "absent" from zero.
// retention_days: null resets to the default, absent leaves it alone.
type cameraIn struct {
	Name          *string          `json:"name"`
	MainURL       *string          `json:"main_url"`
	SubURL        *string          `json:"sub_url"`
	Enabled       *bool            `json:"enabled"`
	RecordMode    *string          `json:"record_mode"`
	RecordAudio   *bool            `json:"record_audio"`
	SegmentSec    *int             `json:"segment_sec"`
	RetentionDays json.RawMessage  `json:"retention_days"`
	MotionMode    *string          `json:"motion_mode"`
	MotionConfig  *json.RawMessage `json:"motion_config"`
}

func (in cameraIn) apply(c *camera.Camera) error {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&c.Name, in.Name)
	set(&c.MainURL, in.MainURL)
	set(&c.SubURL, in.SubURL)
	set(&c.RecordMode, in.RecordMode)
	set(&c.MotionMode, in.MotionMode)
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.RecordAudio != nil {
		c.RecordAudio = *in.RecordAudio
	}
	if in.SegmentSec != nil {
		c.SegmentSec = *in.SegmentSec
	}
	if in.MotionConfig != nil {
		c.MotionConfig = *in.MotionConfig
	}
	if in.RetentionDays != nil {
		if string(in.RetentionDays) == "null" {
			c.RetentionDays = nil
		} else {
			var d int
			if err := json.Unmarshal(in.RetentionDays, &d); err != nil {
				return errors.New("retention_days must be an integer or null")
			}
			c.RetentionDays = &d
		}
	}
	return nil
}

// cameraError maps store errors to HTTP. Validation messages are safe to
// show: they never contain the URL itself.
func (s *Server) cameraError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, camera.ErrNotFound):
		writeError(w, http.StatusNotFound, "camera not found")
	case errors.Is(err, camera.ErrNameTaken):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, camera.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) listCameras(w http.ResponseWriter, r *http.Request) {
	all, err := s.Cameras.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	list := make([]cameraOut, 0, len(all))
	for _, c := range all {
		list = append(list, out(c))
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getCamera(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := s.Cameras.Get(r.Context(), id)
	if err != nil {
		s.cameraError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out(c))
}

func (s *Server) createCamera(w http.ResponseWriter, r *http.Request) {
	var in cameraIn
	if !decode(w, r, &in) {
		return
	}
	if in.Name == nil || in.MainURL == nil {
		writeError(w, http.StatusUnprocessableEntity, "name and main_url are required")
		return
	}
	c := camera.New(*in.Name, *in.MainURL)
	if err := in.apply(&c); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	c, err := s.Cameras.Create(r.Context(), c)
	if err != nil {
		s.cameraError(w, r, err)
		return
	}
	s.Log.Info("camera created", "id", c.ID, "name", c.Name, "by", currentUser(r).Username)
	writeJSON(w, http.StatusCreated, out(c))
}

// patchCamera changes only the fields present in the body.
// ponytail: the recorder picks the change up on the next `jaga start`;
// live reload of the supervisor comes with the WebSocket status hub.
func (s *Server) patchCamera(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in cameraIn
	if !decode(w, r, &in) {
		return
	}
	c, err := s.Cameras.Get(r.Context(), id)
	if err != nil {
		s.cameraError(w, r, err)
		return
	}
	if err := in.apply(&c); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if c, err = s.Cameras.Update(r.Context(), c); err != nil {
		s.cameraError(w, r, err)
		return
	}
	s.Log.Info("camera updated", "id", c.ID, "by", currentUser(r).Username)
	writeJSON(w, http.StatusOK, out(c))
}

func (s *Server) deleteCamera(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Cameras.Delete(r.Context(), id); err != nil {
		s.cameraError(w, r, err)
		return
	}
	s.Log.Info("camera deleted", "id", id, "by", currentUser(r).Username)
	w.WriteHeader(http.StatusNoContent)
}

// testCamera probes the stored main URL (or sub URL with ?stream=sub).
// A camera that cannot be reached is a normal result, not a server error.
func (s *Server) testCamera(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	c, err := s.Cameras.Get(r.Context(), id)
	if err != nil {
		s.cameraError(w, r, err)
		return
	}
	url := c.MainURL
	if r.URL.Query().Get("stream") == "sub" {
		if c.SubURL == "" {
			writeError(w, http.StatusUnprocessableEntity, "camera has no sub_url")
			return
		}
		url = c.SubURL
	}
	res, err := s.probe(r.Context(), s.FFprobe, url, 5*time.Second)
	if errors.Is(err, probe.ErrProbe) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "streams": res.Streams, "sampled_sec": res.Sampled.Seconds()})
}
