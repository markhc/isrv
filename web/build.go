// Package web embeds the built frontend assets.
package web

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:dist
var Dist embed.FS

// DistDirFS is the sub-filesystem rooted at the dist directory.
var DistDirFS = mustSubFS(Dist, "dist")

func mustSubFS(fsys fs.FS, root string) fs.FS {
	sub, err := fs.Sub(fsys, filepath.ToSlash(filepath.Clean(root)))
	if err != nil {
		panic(fmt.Errorf("cannot create sub FS for %q: %w", root, err))
	}
	return sub
}

type devFS struct{}

func (devFS) Open(name string) (fs.File, error) {
	return os.Open(name)
}
