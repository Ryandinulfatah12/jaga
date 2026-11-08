// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

//go:build !webui

package web

import (
	"embed"
	"io/fs"
)

//go:embed placeholder/index.html
var placeholder embed.FS

// Built reports whether the real UI is embedded.
const Built = false

func files() fs.FS {
	root, _ := fs.Sub(placeholder, "placeholder") // cannot fail: the directory is embedded
	return root
}
