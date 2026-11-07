// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// openTemp opens a fresh database in a temp dir and closes it when the test ends.
func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "jaga.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	return db
}

func TestOpenAppliesPragmas(t *testing.T) {
	ctx := t.Context()
	db := openTemp(t)

	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := t.Context()
	db := openTemp(t)

	migrations := fstest.MapFS{
		"00001_widgets.sql": {Data: []byte(`-- +goose Up
CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL);

-- +goose Down
DROP TABLE widgets;
`)},
	}

	for i := range 2 {
		if err := Migrate(ctx, db, migrations); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO widgets (name) VALUES ('a')"); err != nil {
		t.Fatalf("table missing after migrate: %v", err)
	}
}
