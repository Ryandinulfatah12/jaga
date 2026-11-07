// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package migrations holds the SQL migrations embedded into the binary.
package migrations

import "embed"

// FS holds the goose migrations (NNNNN_name.sql). Empty until the first
// migration lands; F1.02 adds `//go:embed *.sql` together with it.
var FS embed.FS
