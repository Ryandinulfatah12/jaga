// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package db opens the SQLite database and applies migrations.
//
// The driver is modernc.org/sqlite (pure Go), so the binary builds with
// CGO_ENABLED=0. Migrations are plain SQL files run by goose.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Pragmas applied to every connection in the pool.
var pragmas = []string{
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
	"foreign_keys(ON)",
	"busy_timeout(5000)",
}

// Open opens (creating if needed) the database at path and checks that it is reachable.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	// BEGIN IMMEDIATE takes the write lock up front, so concurrent writers
	// wait on busy_timeout instead of failing with SQLITE_BUSY mid-transaction.
	q.Set("_txlock", "immediate")

	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// Migrate applies every pending goose migration found at the root of migrations.
func Migrate(ctx context.Context, db *sql.DB, migrations fs.FS) error {
	p, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if errors.Is(err, goose.ErrNoMigrations) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
