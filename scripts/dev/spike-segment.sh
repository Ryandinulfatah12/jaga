#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright The Jaga Authors
#
# Spike: is `-f segment -segment_list pipe:1` a reliable segment index when
# FFmpeg is killed or the camera stalls? Run scripts/dev/rtsp-sim.sh first.
# Results are written up in docs/spikes/segment-index.md.

set -u

base=${SIM_URL:-rtsp://127.0.0.1:8554}
out=${SPIKE_DIR:-.sim/spike}
rm -rf "$out"
mkdir -p "$out"

# The recorder command from docs/03-design.md, with short segments.
record() { # url, output pattern, list file, log file
	ffmpeg -nostdin -hide_banner -loglevel warning \
		-protocol_whitelist rtsp,rtsps,rtp,udp,tcp,tls \
		-rtsp_transport tcp -timeout 5000000 -i "$1" \
		-c copy -map 0:v \
		-f segment -segment_time 4 -segment_format mp4 \
		-segment_format_options movflags=+frag_keyframe+empty_moov \
		-reset_timestamps 1 -strftime 1 \
		-segment_list pipe:1 -segment_list_type csv \
		"$2" >"$3" 2>"$4" &
	rec=$!
}

check() { # file: print duration and packet count, or the ffprobe error
	if r=$(ffprobe -v error -select_streams v -count_packets \
		-show_entries format=duration:stream=nb_read_packets -of csv=p=0 "$1" 2>&1); then
		echo "    ok   $(echo "$r" | tr '\n' ' ') $1"
	else
		echo "    BAD  $1: $(echo "$r" | sed -n 1p)"
	fi
}

report() { # dir, list file
	echo "  segment list:"
	sed 's/^/    | /' "$2"
	echo "  files on disk:"
	find "$1" -name '*.mp4' | sort | while read -r f; do check "$f"; done
}

wait_exit() { # pid, max seconds: prints exit code and time taken
	t=0
	while kill -0 "$1" 2>/dev/null; do
		[ "$t" -ge "$2" ] && { echo "  still running after ${2}s"; return; }
		sleep 1
		t=$((t + 1))
	done
	wait "$1"
	echo "  exited with code $? after ~${t}s"
}

echo "== A: SIGKILL mid-segment"
mkdir -p "$out/a"
record "$base/cam1/main" "$out/a/%H%M%S.mp4" "$out/a.csv" "$out/a.log"
sleep 10
kill -KILL "$rec"
wait "$rec" 2>/dev/null
report "$out/a" "$out/a.csv"

echo "== B: SIGINT (graceful stop)"
mkdir -p "$out/b"
record "$base/cam1/main" "$out/b/%H%M%S.mp4" "$out/b.csv" "$out/b.log"
sleep 10
kill -INT "$rec"
wait_exit "$rec" 10
report "$out/b" "$out/b.csv"

echo "== C: H.265 camera"
mkdir -p "$out/c"
record "$base/cam2/main" "$out/c/%H%M%S.mp4" "$out/c.csv" "$out/c.log"
sleep 10
kill -INT "$rec"
wait_exit "$rec" 10
report "$out/c" "$out/c.csv"

echo "== D: output directory does not exist"
record "$base/cam1/main" "$out/d/%Y-%m-%d/%H%M%S.mp4" "$out/d.csv" "$out/d.log"
wait_exit "$rec" 10
kill -KILL "$rec" 2>/dev/null
sed 's/^/  log: /' "$out/d.log"

echo "== E: strftime directory rolls over while recording (per-minute dirs)"
now=$(date +%H%M)
mkdir -p "$out/e/$now"
record "$base/cam1/main" "$out/e/%H%M/%S.mp4" "$out/e.csv" "$out/e.log"
while [ "$(date +%H%M)" = "$now" ]; do sleep 1; done
wait_exit "$rec" 12
kill -INT "$rec" 2>/dev/null
wait "$rec" 2>/dev/null
report "$out/e" "$out/e.csv"
sed 's/^/  log: /' "$out/e.log"

echo "== F: camera unreachable"
record "rtsp://127.0.0.1:9/cam" "$out/f/%H%M%S.mp4" "$out/f.csv" "$out/f.log"
wait_exit "$rec" 15
sed 's/^/  log: /' "$out/f.log"

echo "== G: unknown path on a live server"
record "$base/nope" "$out/g/%H%M%S.mp4" "$out/g.csv" "$out/g.log"
wait_exit "$rec" 15
sed 's/^/  log: /' "$out/g.log"

echo "== H: camera stalls (publisher SIGSTOP, TCP stays open)"
pub=$(pgrep -f 'cam2-sub.mkv' | sed -n 1p)
mkdir -p "$out/h"
record "$base/cam2/sub" "$out/h/%H%M%S.mp4" "$out/h.csv" "$out/h.log"
sleep 6
kill -STOP "$pub"
start=$(date +%s)
wait_exit "$rec" 40
echo "  (stall began ${start}, recorder gone at $(date +%s))"
kill -CONT "$pub"
kill -KILL "$rec" 2>/dev/null
report "$out/h" "$out/h.csv"
sed 's/^/  log: /' "$out/h.log"
