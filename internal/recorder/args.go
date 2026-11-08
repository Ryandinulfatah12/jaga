// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package recorder builds the FFmpeg command that records one camera.
package recorder

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/ryandinulfatah12/jaga/internal/camera"
)

// SegmentPattern is the strftime pattern FFmpeg expands for each segment,
// relative to the camera's recordings folder. FFmpeg does not create the
// date folder, so the supervisor creates it ahead of time
// (docs/spikes/segment-index.md).
const SegmentPattern = "%Y-%m-%d/%Y%m%d-%H%M%S.mp4"

// ProgressFD is the file descriptor FFmpeg writes -progress output to. The
// supervisor passes a pipe there through exec.Cmd.ExtraFiles (fd 3).
const ProgressFD = 3

// Command is what the supervisor runs: exec.Command(Path, Args...) with Env
// appended to the environment.
type Command struct {
	Path string
	Args []string
	Env  []string
}

// Dir returns the folder holding a camera's recordings.
func Dir(dataDir string, cameraID int64) string {
	return filepath.Join(dataDir, "recordings", strconv.FormatInt(cameraID, 10))
}

// Build returns the FFmpeg command that records c's main stream into
// Dir(dataDir, c.ID). The stream is copied, never transcoded, into
// fragmented MP4 segments, and FFmpeg writes the name of every finished
// segment to stdout as a CSV line.
func Build(ffmpegPath, dataDir string, c camera.Camera) (Command, error) {
	// Re-checked here because this is where the URL meets argv.
	if err := camera.ValidateURL(c.MainURL); err != nil {
		return Command{}, fmt.Errorf("camera %d: %w", c.ID, err)
	}
	if c.ID < 1 {
		return Command{}, fmt.Errorf("camera id %d: not stored yet", c.ID)
	}
	if c.SegmentSec < 1 {
		return Command{}, fmt.Errorf("camera %d: segment_sec %d", c.ID, c.SegmentSec)
	}

	args := []string{
		"-nostdin", "-hide_banner", "-nostats", "-loglevel", "warning",
		// key=value progress blocks on fd 3 feed the supervisor's stall
		// watchdog; stderr stays free for error messages.
		"-progress", "pipe:" + strconv.Itoa(ProgressFD),
		// Only network protocols, so a URL can never read local files.
		"-protocol_whitelist", "rtsp,rtsps,rtp,udp,tcp,tls",
		// -timeout makes FFmpeg exit when a camera stops sending but keeps
		// the TCP connection open (spike scenario H).
		"-rtsp_transport", "tcp", "-timeout", "5000000",
		"-i", c.MainURL,
		"-map", "0:v",
	}
	if c.RecordAudio {
		args = append(args, "-map", "0:a?") // '?': cameras without audio still record
	}
	args = append(args,
		"-c", "copy",
		"-f", "segment",
		"-segment_time", strconv.Itoa(c.SegmentSec),
		"-segment_format", "mp4",
		// Fragmented MP4: a segment cut short by a crash is often still playable.
		"-segment_format_options", "movflags=+frag_keyframe+empty_moov",
		"-reset_timestamps", "1",
		"-strftime", "1",
		"-segment_list", "pipe:1",
		"-segment_list_type", "csv",
		filepath.Join(Dir(dataDir, c.ID), SegmentPattern),
	)

	return Command{
		Path: ffmpegPath,
		Args: args,
		// strftime names in UTC, so DST or timezone changes never collide.
		Env: []string{"TZ=UTC"},
	}, nil
}
