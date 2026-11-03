// Package project finds the source files of a Vela project and maps them to
// output paths.
package project

import (
	"os"
	"path/filepath"
	"strings"
)

// MirrorPath maps  <srcRoot>/a/b/C.vela  →  <outRoot>/a/b/C<ext>
//
// Example:
//
//	src/dashboard/components/Card.vela  →  dist/dashboard/components/Card.js
func MirrorPath(src, srcRoot, outRoot, ext string) string {
	rel, err := filepath.Rel(srcRoot, src)
	if err != nil {
		rel = filepath.Base(src)
	}
	noExt := strings.TrimSuffix(rel, filepath.Ext(rel))
	return filepath.Join(outRoot, noExt+ext)
}

// ComponentID is the $$id of the component in src: its path relative to
// srcRoot without the extension, with forward slashes (routes/users/Page).
// Without a project root, or for a file outside it, it is the file name
// without the extension. The JavaScript factory and the Go renderer of a
// component carry the same ID.
func ComponentID(src, srcRoot string) string {
	if srcRoot != "" {
		if rel, err := filepath.Rel(srcRoot, src); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return filepath.ToSlash(strings.TrimSuffix(rel, filepath.Ext(rel)))
		}
	}
	base := filepath.Base(src)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// CollectVela recursively finds all .vela files under root,
// skipping hidden directories, node_modules, and common output dirs.
func CollectVela(root string) []string {
	skipDirs := map[string]bool{
		"node_modules": true,
		"dist":         true,
		".git":         true,
		".svn":         true,
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// The project root itself is never skipped, so `--project .` works.
			if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".vela") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func CollectJSModules(root string) []string {
	skipDirs := map[string]bool{
		"node_modules": true,
		"dist":         true,
		".git":         true,
		".svn":         true,
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// The project root itself is never skipped, so `--project .` works.
			if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".js") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// RelOrBase returns path relative to root, or just the base name on error.
func RelOrBase(f, root string) string {
	if rel, err := filepath.Rel(root, f); err == nil {
		return rel
	}
	return filepath.Base(f)
}

func ComponentName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
