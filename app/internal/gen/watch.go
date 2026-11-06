package gen

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Watch regenerates the project at root whenever its inputs change: the
// routes tree, the mirrored Go tree, the matchers and the configuration. It
// polls every interval, so it needs no file system notifications, and runs
// until ctx is done. done receives the outcome of every generation,
// starting with the first one.
func Watch(ctx context.Context, root string, interval time.Duration, done func(Result, []Diagnostic)) {
	last := ""
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if current := fingerprint(root); current != last {
			last = current
			done(Generate(root))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// fingerprint hashes the names, sizes and modification times of the inputs.
func fingerprint(root string) string {
	h := sha256.New()
	for _, file := range []string{"vela.config.json", ConfigFile, "go.mod"} {
		stamp(h, filepath.Join(root, file))
	}
	cfg, _ := LoadConfig(root)
	for _, dir := range []string{cfg.Routes, cfg.Server, cfg.Params, cfg.ServerParams} {
		_ = filepath.WalkDir(cfg.abs(dir), func(file string, entry fs.DirEntry, err error) error {
			if err == nil {
				stamp(h, file)
			}
			return nil
		})
	}
	if cfg.TypeScript {
		// The route types follow the Go types of the loaders' results,
		// which other packages of the module may declare (TS-8.3).
		_ = filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := entry.Name()
			if entry.IsDir() && file != root && (name[0] == '.' || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			if !entry.IsDir() && filepath.Ext(name) == ".go" {
				stamp(h, file)
			}
			return nil
		})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func stamp(h interface{ Write([]byte) (int, error) }, file string) {
	info, err := os.Stat(file)
	if err != nil {
		fmt.Fprintf(h, "%s -\n", file)
		return
	}
	fmt.Fprintf(h, "%s %d %d %v\n", file, info.Size(), info.ModTime().UnixNano(), info.IsDir())
}
