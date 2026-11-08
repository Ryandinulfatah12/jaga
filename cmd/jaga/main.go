// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Command jaga is a lightweight self-hosted NVR.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ryandinulfatah12/jaga/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// The first signal starts a graceful shutdown. Unregistering then lets a
	// second Ctrl-C or SIGTERM kill jaga right away if shutdown hangs.
	go func() { <-ctx.Done(); stop() }()
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv)
	stop()
	os.Exit(code)
}
