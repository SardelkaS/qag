package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Assets contains the production frontend. Run scripts/build.ps1 before go build.
func Assets() fs.FS {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("embedded frontend is missing")
	}
	return assets
}
