#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright The Jaga Authors
#
# Usage: probe.sh <rtsp-url> [seconds]
# Prints codec, resolution and fps of each stream, then measures the real
# bitrate and keyframe interval by reading packets for <seconds> (default 10).
# Prototype of what `jaga probe` will do.

set -eu

url=$1
secs=${2:-10}

ffprobe -v error -rtsp_transport tcp \
	-show_entries stream=index,codec_type,codec_name,profile,width,height,avg_frame_rate,sample_rate,channels \
	-of compact=p=0 "$url"

ffprobe -v error -rtsp_transport tcp -read_intervals "%+$secs" \
	-show_entries packet=stream_index,pts_time,size,flags -of compact=p=0 "$url" |
	awk -F'|' '
	{
		for (i = 1; i <= NF; i++) { split($i, kv, "="); f[kv[1]] = kv[2] }
		s = f["stream_index"]; t = f["pts_time"]
		if (t == "N/A") next
		bytes[s] += f["size"]
		if (!(s in first)) first[s] = t
		last[s] = t
		if (f["flags"] ~ /K/) { if (s in lastkey) { gaps[s] += t - lastkey[s]; ngaps[s]++ } lastkey[s] = t }
	}
	END {
		for (s in bytes) {
			d = last[s] - first[s]
			if (d <= 0) continue
			gop = "n/a"
			if (ngaps[s]) gop = sprintf("%.2fs", gaps[s] / ngaps[s])
			if (ngaps[s] && gaps[s] / ngaps[s] < 0.1) gop = "packet (audio)"
			printf "stream %s: %.0f kbps, keyframe every %s (over %.1fs)\n", s, bytes[s] * 8 / d / 1000, gop, d
		}
	}'
