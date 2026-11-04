package web

import (
	"embed"
	"io/fs"
)

//go:embed all:bundle
var embedded embed.FS

// Assets contains the prepared frontend shipped in the Go module itself.
func Assets() fs.FS {
	assets, err := fs.Sub(embedded, "bundle")
	if err != nil {
		panic("embedded frontend is missing")
	}
	return assets
}
