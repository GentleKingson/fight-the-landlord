//go:build webui

package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distribution embed.FS

func assets() (fs.FS, error) {
	return fs.Sub(distribution, "dist")
}
