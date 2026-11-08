// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package probe

import (
	"context"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/camera"
)

func TestParseRealOutput(t *testing.T) {
	// Captured from ffprobe against a mediamtx simulator (H.264 640x360 25 fps + AAC 16 kHz mono).
	b, err := os.ReadFile("testdata/mediamtx-h264-aac.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Streams) != 2 {
		t.Fatalf("streams = %+v", res.Streams)
	}
	v, a := res.Streams[0], res.Streams[1]
	if v.Type != "video" || v.Codec != "h264" || v.Width != 640 || v.Height != 360 || v.FPS != 25 || v.Profile != "Constrained Baseline" {
		t.Errorf("video = %+v", v)
	}
	if a.Type != "audio" || a.Codec != "aac" || a.SampleRate != 16000 || a.Channels != 1 || a.GOPSeconds != 0 {
		t.Errorf("audio = %+v", a)
	}
	if v.BitrateKbps < 100 || v.BitrateKbps > 2000 || a.BitrateKbps < 10 || a.BitrateKbps > 200 {
		t.Errorf("bitrates video=%d audio=%d kbps", v.BitrateKbps, a.BitrateKbps)
	}
	if res.Sampled < 2*time.Second {
		t.Errorf("sampled = %v", res.Sampled)
	}
}

func TestParseMeasuresGOPAndBitrate(t *testing.T) {
	// 4 s of video: 100 kB per second, keyframe every 2 s; one packet without pts.
	var pk []string
	for i := 0; i <= 40; i++ {
		flags := "___"
		if i%20 == 0 {
			flags = "K__"
		}
		pk = append(pk, `{"stream_index":0,"pts_time":"`+strconv.FormatFloat(float64(i)/10, 'f', 6, 64)+`","size":"10000","flags":"`+flags+`"}`)
	}
	pk = append(pk, `{"stream_index":0,"size":"999999","flags":"K__"}`)
	b := `{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,"avg_frame_rate":"30000/1001"}],"packets":[` + strings.Join(pk, ",") + `]}`
	res, err := parse([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	s := res.Streams[0]
	if s.GOPSeconds != 2 || math.Abs(s.FPS-29.97) > 0.01 {
		t.Errorf("gop=%v fps=%v", s.GOPSeconds, s.FPS)
	}
	// 41 packets of 10 kB over 4 s = 820 kbps (the pts-less packet is ignored).
	if s.BitrateKbps != 820 {
		t.Errorf("bitrate = %d kbps", s.BitrateKbps)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := parse([]byte("not json")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestRate(t *testing.T) {
	for in, want := range map[string]float64{"25/1": 25, "0/0": 0, "": 0, "x/y": 0, "10/0": 0} {
		if got := rate(in); got != want {
			t.Errorf("rate(%q) = %v", in, got)
		}
	}
}

func TestReasonMasksCredentials(t *testing.T) {
	got := reason("[rtsp @ 0x1] method DESCRIBE failed: 404 (Not Found)\nrtsp://admin:hunter2@cam/nope: Server returned 404 Not Found\n", errors.New("exit status 1"))
	if got != "rtsp://***@cam/nope: Server returned 404 Not Found" {
		t.Errorf("reason = %q", got)
	}
	if got := reason("  \n", errors.New("exit status 1")); got != "exit status 1" {
		t.Errorf("empty stderr reason = %q", got)
	}
}

func TestProbeValidatesURLFirst(t *testing.T) {
	// ffprobe is never run for a bad URL: "definitely-missing" would fail differently.
	_, err := Probe(context.Background(), "definitely-missing-ffprobe", "file:///etc/passwd", time.Second)
	if !errors.Is(err, camera.ErrInvalidURL) {
		t.Errorf("err = %v, want ErrInvalidURL", err)
	}
	_, err = Probe(context.Background(), "definitely-missing-ffprobe", "rtsp://cam/s", time.Second)
	if !errors.Is(err, ErrProbe) {
		t.Errorf("missing ffprobe: err = %v, want ErrProbe", err)
	}
}
