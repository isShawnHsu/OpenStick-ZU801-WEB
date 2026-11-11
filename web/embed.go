// Package web embeds the static web interface.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// Static returns the embedded static directory.
func Static() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
