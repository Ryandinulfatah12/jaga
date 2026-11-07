// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package migrations holds the SQL migrations embedded into the binary.
package migrations

import "embed"

// FS holds the goose migrations (NNNNN_name.sql), applied by db.Migrate.
//
//go:embed *.sql
var FS embed.FS
