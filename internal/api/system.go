// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package api

import (
	"bufio"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/retention"
	"github.com/ryandinulfatah12/jaga/internal/version"
)

type systemOut struct {
	Version string    `json:"version"`
	Commit  string    `json:"commit"`
	Tools   []string  `json:"tools"` // e.g. "ffmpeg 9.0.2 (/usr/local/bin/ffmpeg)"
	Started time.Time `json:"started"`
	Go      string    `json:"go"`

	CPUs    int        `json:"cpus"`
	Load    [3]float64 `json:"load"`                // 1, 5, 15 min; Linux only
	MemUsed uint64     `json:"mem_used,omitempty"`  // host, bytes; Linux only
	MemTot  uint64     `json:"mem_total,omitempty"` // host
	RSS     uint64     `json:"rss,omitempty"`       // this process

	Disk struct {
		Used         uint64 `json:"used"`
		Total        uint64 `json:"total"`
		QuotaPercent int    `json:"quota_percent"`
	} `json:"disk"`
	Recordings []recStats `json:"recordings"`
}

type recStats struct {
	CameraID int64 `json:"camera_id"`
	Segments int64 `json:"segments"`
	Bytes    int64 `json:"bytes"`
	Oldest   int64 `json:"oldest"`   // Unix ms, 0 when none
	Last24h  int64 `json:"last_24h"` // bytes recorded in the last 24 h
}

// system answers GET /system: versions, host load, disk and how much each
// camera has recorded. Everything is read on request; nothing is cached.
func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	out := systemOut{Version: version.Version, Commit: version.Commit, Tools: s.Tools, Started: s.Started,
		Go: runtime.Version(), CPUs: runtime.NumCPU(), Recordings: []recStats{}}
	if out.Tools == nil {
		out.Tools = []string{}
	}
	out.Load = loadAvg()
	out.MemTot, out.MemUsed = memInfo()
	out.RSS = selfRSS()
	if used, avail, err := retention.DiskUsage(s.DataDir); err == nil {
		out.Disk.Used, out.Disk.Total = used, used+avail
	}
	out.Disk.QuotaPercent = s.QuotaPercent

	since := time.Now().Add(-24 * time.Hour).UnixMilli()
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT camera_id, count(*), coalesce(sum(size_bytes), 0), coalesce(min(start_ts), 0),
			coalesce(sum(CASE WHEN start_ts >= ? THEN size_bytes END), 0)
		FROM segments GROUP BY camera_id ORDER BY camera_id`, since)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer rows.Close() //nolint:errcheck // rows.Err below
	for rows.Next() {
		var st recStats
		if err := rows.Scan(&st.CameraID, &st.Segments, &st.Bytes, &st.Oldest, &st.Last24h); err != nil {
			s.internal(w, r, err)
			return
		}
		out.Recordings = append(out.Recordings, st)
	}
	if err := rows.Err(); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ponytail: /proc readers are Linux only (the Docker image and the main
// target); other systems get zeros. Add per-OS files if a port needs them.
func loadAvg() (l [3]float64) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return l
	}
	for i, f := range strings.Fields(string(b)) {
		if i == 3 {
			break
		}
		l[i], _ = strconv.ParseFloat(f, 64)
	}
	return l
}

// memInfo returns host total and used (total minus MemAvailable) in bytes.
func memInfo() (total, used uint64) {
	m := procKB("/proc/meminfo", "MemTotal:", "MemAvailable:")
	if m[0] == 0 {
		return 0, 0
	}
	return m[0], m[0] - min(m[1], m[0])
}

func selfRSS() uint64 { return procKB("/proc/self/status", "VmRSS:")[0] }

// procKB reads "Key:   123 kB" lines and returns the values in bytes, in
// the order of keys.
func procKB(path string, keys ...string) []uint64 {
	out := make([]uint64, len(keys))
	f, err := os.Open(path) //nolint:gosec // fixed /proc paths
	if err != nil {
		return out
	}
	defer f.Close() //nolint:errcheck // read only
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		for i, k := range keys {
			if len(fields) >= 2 && fields[0] == k {
				v, _ := strconv.ParseUint(fields[1], 10, 64)
				out[i] = v * 1024
			}
		}
	}
	return out
}
