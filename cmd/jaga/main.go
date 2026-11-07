// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Command jaga is a lightweight self-hosted NVR.
//
// Subcommands (start, init, migrate, ...) arrive in phase 1; for now the
// binary only reports its version so the build pipeline has something to ship.
package main

import (
	"fmt"

	"github.com/ryandinulfatah12/jaga/internal/version"
)

func main() {
	fmt.Println(version.String())
}
