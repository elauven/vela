// Package vite writes the <script> and <link> tags of a client that Vite
// serves or builds, for pages rendered by the generated Go renderers
// (docs/SSR.md "Development and production with Vite").
//
// In development the tags load the entry modules from the Vite dev server;
// in production they name the hashed files of `vite build`, read from its
// manifest (build.manifest), as Vite's backend integration describes.
package vite

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"sort"
	"strings"
)

// Assets gives the tags of client entries. An entry is a path relative to
// the Vite root, such as "src/client.js", the key of the manifest.
type Assets struct {
	dev      string
	base     string
	manifest map[string]chunk
}

type chunk struct {
	File    string   `json:"file"`
	IsEntry bool     `json:"isEntry"`
	Imports []string `json:"imports"`
	CSS     []string `json:"css"`
}

// Dev returns the assets of the Vite dev server at serverURL, such as
// "http://localhost:5173": the Vite client and the entry modules, which
// the server compiles on request.
func Dev(serverURL string) *Assets {
	return &Assets{dev: strings.TrimSuffix(serverURL, "/")}
}

// Load reads the manifest of a build, dist/.vite/manifest.json; base is
// the public path the build is served at (Vite's base, "/" by default).
func Load(manifestPath, base string) (*Assets, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("vite: %w (build with build.manifest: true)", err)
	}
	return Parse(data, base)
}

// Parse is Load for the manifest's content.
func Parse(manifest []byte, base string) (*Assets, error) {
	a := &Assets{base: base}
	if err := json.Unmarshal(manifest, &a.manifest); err != nil {
		return nil, fmt.Errorf("vite: manifest: %w", err)
	}
	if !strings.HasSuffix(a.base, "/") {
		a.base += "/"
	}
	return a, nil
}

// Tags returns the tags of the entries for the document head:
//
//   - development: the Vite client and a module script per entry;
//   - production: the stylesheets of the entries and of the chunks they
//     import, a modulepreload link per imported chunk, and a module script
//     per entry. Each file appears once.
func (a *Assets) Tags(entries ...string) (string, error) {
	var b strings.Builder
	if a.dev != "" {
		writeScript(&b, a.dev+"/@vite/client")
		for _, entry := range entries {
			writeScript(&b, a.dev+"/"+strings.TrimPrefix(entry, "/"))
		}
		return b.String(), nil
	}
	var css, preload, scripts []string
	seen := map[string]bool{}
	add := func(list *[]string, file string) {
		if !seen[file] {
			seen[file] = true
			*list = append(*list, file)
		}
	}
	visited := map[string]bool{}
	var imported func(key string)
	imported = func(key string) {
		if visited[key] {
			return
		}
		visited[key] = true
		c := a.manifest[key]
		for _, file := range c.CSS {
			add(&css, file)
		}
		for _, imp := range c.Imports {
			imported(imp)
			add(&preload, a.manifest[imp].File)
		}
	}
	for _, entry := range entries {
		entry = strings.TrimPrefix(entry, "/")
		c, ok := a.manifest[entry]
		if !ok || !c.IsEntry {
			return "", fmt.Errorf("vite: %q is not an entry of the manifest; entries: %s", entry, strings.Join(a.entries(), ", "))
		}
		imported(entry)
		add(&scripts, c.File)
	}
	for _, file := range css {
		fmt.Fprintf(&b, `<link rel="stylesheet" href="%s">`, html.EscapeString(a.base+file))
	}
	for _, file := range preload {
		fmt.Fprintf(&b, `<link rel="modulepreload" href="%s">`, html.EscapeString(a.base+file))
	}
	for _, file := range scripts {
		writeScript(&b, a.base+file)
	}
	return b.String(), nil
}

func (a *Assets) entries() []string {
	var out []string
	for key, c := range a.manifest {
		if c.IsEntry {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func writeScript(b *strings.Builder, src string) {
	fmt.Fprintf(b, `<script type="module" src="%s"></script>`, html.EscapeString(src))
}
