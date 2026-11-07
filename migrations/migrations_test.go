// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package migrations_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryandinulfatah12/jaga/internal/db"
	"github.com/ryandinulfatah12/jaga/migrations"
)

func TestInitialSchema(t *testing.T) {
	ctx := t.Context()
	conn, err := db.Open(ctx, filepath.Join(t.TempDir(), "jaga.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.Migrate(ctx, conn, migrations.FS); err != nil {
		t.Fatal(err)
	}

	exec := func(q string, args ...any) error {
		_, err := conn.ExecContext(ctx, q, args...)
		return err
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	mustExec(`INSERT INTO users (username, password_hash, role) VALUES ('admin', 'x', 'admin')`)
	mustExec(`INSERT INTO cameras (name, main_url) VALUES ('depan', x'00')`)
	mustExec(`INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes) VALUES (1, 'recordings/1/a.mp4', 0, 600000, 10)`)
	mustExec(`INSERT INTO events (camera_id, type, start_ts) VALUES (1, 'motion', 0)`)
	mustExec(`INSERT INTO events (type, start_ts) VALUES ('disk_full', 0)`)
	mustExec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (x'01', 1, 0)`)

	var mode string
	var segSec int
	if err := conn.QueryRowContext(ctx, `SELECT record_mode, segment_sec FROM cameras WHERE id = 1`).Scan(&mode, &segSec); err != nil {
		t.Fatal(err)
	}
	if mode != "continuous" || segSec != 600 {
		t.Errorf("camera defaults: record_mode=%q segment_sec=%d", mode, segSec)
	}

	for name, q := range map[string]string{
		"duplicate username (case)": `INSERT INTO users (username, password_hash, role) VALUES ('ADMIN', 'x', 'viewer')`,
		"unknown role":              `INSERT INTO users (username, password_hash, role) VALUES ('b', 'x', 'root')`,
		"duplicate camera name":     `INSERT INTO cameras (name, main_url) VALUES ('Depan', x'00')`,
		"empty camera name":         `INSERT INTO cameras (name, main_url) VALUES ('', x'00')`,
		"unknown motion mode":       `INSERT INTO cameras (name, main_url, motion_mode) VALUES ('c', x'00', 'ai')`,
		"motion_config not json":    `INSERT INTO cameras (name, main_url, motion_config) VALUES ('d', x'00', 'nope')`,
		"segment too short":         `INSERT INTO cameras (name, main_url, segment_sec) VALUES ('e', x'00', 1)`,
		"text in integer (STRICT)":  `INSERT INTO cameras (name, main_url, enabled) VALUES ('f', x'00', 'yes')`,
		"segment without camera":    `INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes) VALUES (99, 'x.mp4', 0, 1, 0)`,
		"segment ends before start": `INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes) VALUES (1, 'y.mp4', 10, 5, 0)`,
		"duplicate segment path":    `INSERT INTO segments (camera_id, path, start_ts, end_ts, size_bytes) VALUES (1, 'recordings/1/a.mp4', 0, 1, 0)`,
		"unknown event type":        `INSERT INTO events (camera_id, type, start_ts) VALUES (1, 'party', 0)`,
		"unknown notifier kind":     `INSERT INTO notifiers (kind) VALUES ('email')`,
	} {
		if err := exec(q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// Deleting a camera removes its segments and events, keeps system events.
	mustExec(`DELETE FROM cameras WHERE id = 1`)
	var segs, events int
	if err := conn.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM segments), (SELECT count(*) FROM events)`).Scan(&segs, &events); err != nil {
		t.Fatal(err)
	}
	if segs != 0 || events != 1 {
		t.Errorf("after camera delete: segments=%d events=%d, want 0 and 1", segs, events)
	}

	// Deleting a user ends their sessions.
	mustExec(`DELETE FROM users WHERE id = 1`)
	var sessions int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Errorf("sessions left after user delete: %d", sessions)
	}
}

func TestMigrationsHaveSPDXHeader(t *testing.T) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(b), "-- SPDX-License-Identifier: Apache-2.0") {
			t.Errorf("%s: missing SPDX header", e.Name())
		}
	}
}
