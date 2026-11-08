// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package recorder

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FFprobePath assumes ffprobe sits next to ffmpeg, as in every FFmpeg build.
// ponytail: add an ffprobe_path setting if a setup ever splits them.
func FFprobePath(ffmpeg string) string {
	if !strings.ContainsRune(ffmpeg, filepath.Separator) {
		return "ffprobe"
	}
	return filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
}

// Duration returns the duration of a media file as reported by ffprobe.
func Duration(ctx context.Context, ffprobe, path string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", //nolint:gosec // path is a segment under data_dir
		"-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, err
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec < 0 {
		return 0, fmt.Errorf("ffprobe duration %q", strings.TrimSpace(string(out)))
	}
	return time.Duration(sec * float64(time.Second)), nil
}
