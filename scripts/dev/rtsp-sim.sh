#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright The Jaga Authors
#
# RTSP camera simulator for development: mediamtx + looping test clips.
# Needs ffmpeg (with libx264/libx265) and mediamtx on PATH. Ctrl-C stops everything.
#
#   rtsp://127.0.0.1:8554/cam1/main  1920x1080 25 fps H.264 + AAC, ~2 Mbps, GOP 2 s
#   rtsp://127.0.0.1:8554/cam1/sub    640x360  25 fps H.264,        ~512 kbps
#   rtsp://127.0.0.1:8554/cam2/main  1920x1080 20 fps H.265,        ~1.5 Mbps, GOP 2 s
#   rtsp://127.0.0.1:8554/cam2/sub    640x360  10 fps H.264,        ~256 kbps

set -eu

dir=${SIM_DIR:-.sim}
port=${SIM_PORT:-8554}
mkdir -p "$dir"

clip() { # name, then ffmpeg encode args
	name=$1
	shift
	[ -f "$dir/$name.mkv" ] && return
	echo "generating $dir/$name.mkv"
	ffmpeg -nostdin -v error -y "$@" -t 60 "$dir/$name.mkv"
}

clip cam1-main -f lavfi -i testsrc2=size=1920x1080:rate=25 -f lavfi -i sine=frequency=440:sample_rate=16000 \
	-c:v libx264 -preset veryfast -profile:v main -pix_fmt yuv420p -b:v 2M -maxrate 2M -bufsize 4M -g 50 -keyint_min 50 -sc_threshold 0 \
	-c:a aac -b:a 32k
clip cam1-sub -f lavfi -i testsrc2=size=640x360:rate=25 \
	-c:v libx264 -preset veryfast -profile:v main -pix_fmt yuv420p -b:v 512k -maxrate 512k -bufsize 1M -g 50 -keyint_min 50 -sc_threshold 0
clip cam2-main -f lavfi -i testsrc=size=1920x1080:rate=20 \
	-c:v libx265 -preset veryfast -pix_fmt yuv420p -b:v 1500k -x265-params log-level=error:keyint=40:min-keyint=40:scenecut=0
clip cam2-sub -f lavfi -i testsrc=size=640x360:rate=10 \
	-c:v libx264 -preset veryfast -profile:v baseline -pix_fmt yuv420p -b:v 256k -g 20 -keyint_min 20 -sc_threshold 0

cat >"$dir/mediamtx.yml" <<EOF
logLevel: warn
rtspAddress: :$port
rtmp: no
hls: no
webrtc: no
srt: no
paths:
  all_others:
EOF

pids=""
cleanup() {
	trap - EXIT INT TERM
	# shellcheck disable=SC2086
	kill $pids 2>/dev/null || true
	wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

mediamtx "$dir/mediamtx.yml" &
pids="$pids $!"
sleep 1

for cam in cam1 cam2; do
	for stream in main sub; do
		ffmpeg -nostdin -v error -re -stream_loop -1 -i "$dir/$cam-$stream.mkv" -c copy \
			-f rtsp -rtsp_transport tcp "rtsp://127.0.0.1:$port/$cam/$stream" &
		pids="$pids $!"
		echo "publishing rtsp://127.0.0.1:$port/$cam/$stream"
	done
done

wait
