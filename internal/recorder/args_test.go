// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package recorder

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ryandinulfatah12/jaga/internal/camera"
)

func cam() camera.Camera {
	c := camera.New("Depan", "rtsp://admin:pass@192.168.0.60:554/stream1")
	c.ID = 7
	return c
}

func TestBuild(t *testing.T) {
	cmd, err := Build("/usr/bin/ffmpeg", "/data", cam())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-nostdin", "-hide_banner", "-nostats", "-loglevel", "warning",
		"-progress", "pipe:3",
		"-protocol_whitelist", "rtsp,rtsps,rtp,udp,tcp,tls",
		"-rtsp_transport", "tcp", "-timeout", "5000000",
		"-i", "rtsp://admin:pass@192.168.0.60:554/stream1",
		"-map", "0:v",
		"-c:v", "copy",
		"-f", "segment",
		"-segment_time", "600",
		"-segment_format", "mp4",
		"-segment_format_options", "movflags=+frag_keyframe+empty_moov",
		"-reset_timestamps", "1",
		"-strftime", "1",
		"-segment_list", "pipe:1",
		"-segment_list_type", "csv",
		"/data/recordings/7/%Y-%m-%d/%Y%m%d-%H%M%S.mp4",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args:\n got %q\nwant %q", cmd.Args, want)
	}
	if cmd.Path != "/usr/bin/ffmpeg" || !slices.Equal(cmd.Env, []string{"TZ=UTC"}) {
		t.Errorf("path %q env %q", cmd.Path, cmd.Env)
	}
}

func TestBuildAudioAndSegment(t *testing.T) {
	c := cam()
	c.RecordAudio, c.SegmentSec = true, 300
	cmd, err := Build("ffmpeg", "data", c)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "-map 0:v -map 0:a? -c:v copy -c:a aac -b:a 64k") || !strings.Contains(joined, "-segment_time 300") {
		t.Errorf("args: %s", joined)
	}
	// Video is never transcoded; only audio is re-encoded (G.711 cannot go into MP4).
	if strings.Contains(joined, "-c:v libx") || strings.Contains(joined, "-preset") || strings.Contains(joined, "-c copy") {
		t.Errorf("unexpected codec options: %s", joined)
	}
}

func TestBuildURLStaysOneArgument(t *testing.T) {
	// A valid URL with '&' and '?' must stay exactly one argv entry, right after -i.
	c := cam()
	c.MainURL = "rtsp://cam/live?channel=1&subtype=0"
	cmd, err := Build("ffmpeg", "/data", c)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(cmd.Args, "-i")
	if i < 0 || cmd.Args[i+1] != c.MainURL || slices.Index(cmd.Args, c.MainURL) != i+1 {
		t.Errorf("URL not passed as the single -i argument: %q", cmd.Args)
	}
}

func TestBuildRejects(t *testing.T) {
	for name, mutate := range map[string]func(*camera.Camera){
		"file url":       func(c *camera.Camera) { c.MainURL = "file:///etc/passwd" },
		"option url":     func(c *camera.Camera) { c.MainURL = "-i /etc/passwd" },
		"newline url":    func(c *camera.Camera) { c.MainURL = "rtsp://cam/s\n-f null" },
		"unsaved camera": func(c *camera.Camera) { c.ID = 0 },
		"no segment":     func(c *camera.Camera) { c.SegmentSec = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := cam()
			mutate(&c)
			if _, err := Build("ffmpeg", "/data", c); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	c := cam()
	c.MainURL = "concat:/a|/b"
	if _, err := Build("ffmpeg", "/data", c); !errors.Is(err, camera.ErrInvalidURL) {
		t.Errorf("want ErrInvalidURL, got %v", err)
	}
}

func TestDir(t *testing.T) {
	if got := Dir("/data", 12); got != "/data/recordings/12" {
		t.Errorf("Dir = %q", got)
	}
}

func TestFFprobePath(t *testing.T) {
	if got := FFprobePath("/usr/bin/ffmpeg"); got != "/usr/bin/ffprobe" {
		t.Errorf("FFprobePath = %q", got)
	}
	if got := FFprobePath("ffmpeg"); got != "ffprobe" {
		t.Errorf("FFprobePath bare = %q", got)
	}
}
