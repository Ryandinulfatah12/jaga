// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

//go:build webui

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Built reports whether the real UI is embedded.
const Built = true

func files() fs.FS {
	root, _ := fs.Sub(dist, "dist") // cannot fail: the directory is embedded
	return root
}
