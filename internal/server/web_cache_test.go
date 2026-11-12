package server

import "testing"

func TestHashedWebAsset(t *testing.T) {
	for path, want := range map[string]bool{
		"/assets/index-Ab12_cd3.js":     true,
		"/assets/scene-table-1600.webp": false,
		"/index.html":                   false,
		"/assets/index.js":              false,
		"/api/private-Ab12_cd3.json":    false,
	} {
		if got := hashedWebAsset.MatchString(path); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
}
