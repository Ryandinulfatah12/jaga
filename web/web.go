// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package web serves the web UI. Built with the `webui` tag (`make build`
// after `make web`), the binary embeds web/dist; without it a placeholder
// page is served, so Go builds and tests never need Node.
package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves the UI: files from the build, and index.html for every
// other path so a reload on a client route still loads the app. Hashed
// assets are cached for a year, index.html never (it names the current
// hashes).
func Handler() http.Handler {
	root := files()
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r, root)
			return
		}
		if _, err := fs.Stat(root, name); err != nil {
			serveIndex(w, r, root)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	b, err := fs.ReadFile(root, "index.html")
	if err != nil {
		http.Error(w, "web UI not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodGet {
		_, _ = w.Write(b)
	}
}
