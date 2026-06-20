// Package web embeds the built frontend assets.
package web

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
)

//go:embed all:dist
var Dist embed.FS
var DistDirFS = mustSubFS(Dist, "dist")

func mustSubFS(fsys fs.FS, root string) fs.FS {
	sub, err := fs.Sub(fsys, filepath.ToSlash(filepath.Clean(root)))
	if err != nil {
		panic(fmt.Errorf("cannot create sub FS for %q: %w", root, err))
	}
	return sub
}
