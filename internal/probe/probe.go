// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package probe inspects a camera URL with ffprobe: codecs, resolution, fps,
// and the measured bitrate and keyframe interval. It backs `jaga probe` and
// the POST /cameras/{id}/test endpoint (phase 2).
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
)

// Stream is one audio or video stream of the camera.
type Stream struct {
	Index      int     `json:"index"`
	Type       string  `json:"type"` // video | audio | data
	Codec      string  `json:"codec"`
	Profile    string  `json:"profile,omitempty"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	FPS        float64 `json:"fps,omitempty"`
	SampleRate int     `json:"sample_rate,omitempty"`
	Channels   int     `json:"channels,omitempty"`

	// Measured over the sampled packets; 0 when there were too few.
	BitrateKbps int     `json:"bitrate_kbps,omitempty"`
	GOPSeconds  float64 `json:"gop_seconds,omitempty"` // video keyframe interval
}

// Result is what a probe found.
type Result struct {
	Streams []Stream      `json:"streams"`
	Sampled time.Duration `json:"sampled_ns"`
}

// ErrProbe wraps every failure to read the camera. The message is FFmpeg's,
// with credentials masked.
var ErrProbe = errors.New("probe failed")

// Probe reads url for about sample seconds. The URL is validated first, so it
// can come straight from a user.
func Probe(ctx context.Context, ffprobe, url string, sample time.Duration) (Result, error) {
	if err := camera.ValidateURL(url); err != nil {
		return Result{}, err
	}
	secs := max(1, int(sample.Seconds()))
	// The connection itself is bounded by -timeout; this bounds a camera that
	// connects but sends nothing.
	ctx, cancel := context.WithTimeout(ctx, sample+20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffprobe, //nolint:gosec // URL validated above, single argv entry
		"-v", "error",
		"-protocol_whitelist", "rtsp,rtsps,rtp,udp,tcp,tls",
		"-rtsp_transport", "tcp", "-timeout", "5000000",
		"-read_intervals", "%+"+strconv.Itoa(secs),
		"-show_entries", "stream=index,codec_type,codec_name,profile,width,height,avg_frame_rate,sample_rate,channels:packet=stream_index,pts_time,size,flags",
		"-of", "json",
		url)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("%w: no answer within %s", ErrProbe, sample+20*time.Second)
	}
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s", ErrProbe, reason(stderr.String(), err))
	}
	res, err := parse(stdout.Bytes())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrProbe, err)
	}
	if len(res.Streams) == 0 {
		return Result{}, fmt.Errorf("%w: no streams", ErrProbe)
	}
	return res, nil
}

type rawOutput struct {
	Streams []struct {
		Index        int    `json:"index"`
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Profile      string `json:"profile"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		SampleRate   string `json:"sample_rate"`
		Channels     int    `json:"channels"`
	} `json:"streams"`
	Packets []struct {
		StreamIndex int    `json:"stream_index"`
		PTSTime     string `json:"pts_time"` // absent on some packets
		Size        string `json:"size"`
		Flags       string `json:"flags"`
	} `json:"packets"`
}

// parse turns ffprobe's JSON into a Result, measuring bitrate and keyframe
// interval per stream from the packets.
func parse(b []byte) (Result, error) {
	var raw rawOutput
	if err := json.Unmarshal(b, &raw); err != nil {
		return Result{}, fmt.Errorf("ffprobe output: %w", err)
	}
	type acc struct {
		bytes                int64
		first, last, lastKey float64
		gaps                 float64
		nGaps                int
		seen, seenKey        bool
	}
	per := map[int]*acc{}
	var span float64
	for _, p := range raw.Packets {
		t, err := strconv.ParseFloat(p.PTSTime, 64)
		if err != nil {
			continue // packets without pts are not placed in time
		}
		size, _ := strconv.ParseInt(p.Size, 10, 64)
		a := per[p.StreamIndex]
		if a == nil {
			a = &acc{}
			per[p.StreamIndex] = a
		}
		a.bytes += size
		if !a.seen {
			a.first, a.seen = t, true
		}
		a.last = t
		if strings.HasPrefix(p.Flags, "K") {
			if a.seenKey {
				a.gaps += t - a.lastKey
				a.nGaps++
			}
			a.lastKey, a.seenKey = t, true
		}
		span = max(span, a.last-a.first)
	}

	res := Result{Sampled: time.Duration(span * float64(time.Second))}
	for _, s := range raw.Streams {
		st := Stream{
			Index: s.Index, Type: s.CodecType, Codec: s.CodecName, Profile: s.Profile,
			Width: s.Width, Height: s.Height, Channels: s.Channels, FPS: rate(s.AvgFrameRate),
		}
		st.SampleRate, _ = strconv.Atoi(s.SampleRate)
		if a := per[s.Index]; a != nil {
			if d := a.last - a.first; d > 0 {
				st.BitrateKbps = int(float64(a.bytes) * 8 / d / 1000)
			}
			// Audio packets are all keyframes; a GOP is a video notion.
			if s.CodecType == "video" && a.nGaps > 0 {
				st.GOPSeconds = a.gaps / float64(a.nGaps)
			}
		}
		res.Streams = append(res.Streams, st)
	}
	return res, nil
}

// rate parses "25/1" or "30000/1001"; "0/0" and garbage give 0.
func rate(s string) float64 {
	n, d, ok := strings.Cut(s, "/")
	num, err1 := strconv.ParseFloat(n, 64)
	den, err2 := strconv.ParseFloat(d, 64)
	if !ok || err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	return num / den
}

// reason picks FFmpeg's last stderr line, with credentials masked.
func reason(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return err.Error()
	}
	return camera.MaskText(last)
}
