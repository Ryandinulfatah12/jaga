// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package version holds build information injected at link time.
package version

import "fmt"

// Set with -ldflags "-X github.com/ryandinulfatah12/jaga/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = "none"
)

// String returns a one-line description such as "jaga v0.1.0 (abc1234)".
func String() string {
	return fmt.Sprintf("jaga %s (%s)", Version, Commit)
}
