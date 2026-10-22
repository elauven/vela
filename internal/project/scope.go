package project

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScopeID is the scope class of a component's CSS: a hash of its path
// relative to the working directory, with forward slashes. The client
// module and the Go renderer of a component take it from here, so a build,
// `vela serve` and a bundler plugin running in the same directory give the
// same classes however the path is spelled. A path outside the working
// directory is hashed as an absolute path.
func ScopeID(path string) string {
	hash := md5.Sum([]byte(scopePath(path)))
	return fmt.Sprintf("vela-%x", hash[:3])
}

func scopePath(path string) string {
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}
