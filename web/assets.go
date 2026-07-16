// Package webui exposes the production web client to the Go server.
package webui

import (
	"errors"
	"io/fs"
)

// ErrNotEmbedded is returned by development builds that do not include the
// webui build tag. Local web development is served by Vite instead.
var ErrNotEmbedded = errors.New("web client assets are not embedded")

// Assets returns the built Vite distribution when the binary was compiled
// with -tags=webui.
func Assets() (fs.FS, error) {
	return assets()
}
