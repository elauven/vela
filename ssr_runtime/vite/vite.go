// Package vite writes the <script> and <link> tags of a client that Vite
// serves or builds, for pages rendered by the generated Go renderers
// (docs/SSR.md "Development and production with Vite").
//
// In development the tags load the entry modules from the Vite dev server;
// in production they name the hashed files of `vite build`, read from its
// manifest (build.manifest), as Vite's backend integration describes.
package vite

import (
	"crypto/sha256"
	"encoding/base64"
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
	build    string
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
	sum := sha256.Sum256(manifest)
	a := &Assets{base: base, build: base64.RawURLEncoding.EncodeToString(sum[:9])}
	if err := json.Unmarshal(manifest, &a.manifest); err != nil {
		return nil, fmt.Errorf("vite: manifest: %w", err)
	}
	if !strings.HasSuffix(a.base, "/") {
		a.base += "/"
	}
	return a, nil
}

// BuildID identifies the build: "dev" for the dev server, otherwise a hash
// of the manifest, which names every hashed file of the build. Clients of
// another build reload (docs/APP.md, task 078).
func (a *Assets) BuildID() string {
	if a.dev != "" {
		return "dev"
	}
	return a.build
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
	w := newWalk(a)
	var scripts []string
	for _, entry := range entries {
		entry = strings.TrimPrefix(entry, "/")
		c, ok := a.manifest[entry]
		if !ok || !c.IsEntry {
			return "", fmt.Errorf("vite: %q is not an entry of the manifest; entries: %s", entry, strings.Join(a.entries(), ", "))
		}
		w.imported(entry)
		if !w.seen[c.File] {
			w.seen[c.File] = true
			scripts = append(scripts, c.File)
		}
	}
	w.write(&b, a.base)
	for _, file := range scripts {
		writeScript(&b, a.base+file)
	}
	return b.String(), nil
}

// Preload returns the tags that load the modules a page imports
// dynamically, such as the lazy components it renders, before the client
// asks for them: the stylesheets of each module and of the chunks it
// imports, and a modulepreload link per chunk. A module is a path relative
// to the Vite root, such as "src/routes/Page.vela". Files the entries load
// (the ones passed to Tags) are left out. In development it returns
// nothing: the dev server serves modules on request.
func (a *Assets) Preload(entries []string, modules ...string) (string, error) {
	if a.dev != "" || len(modules) == 0 {
		return "", nil
	}
	loaded := newWalk(a)
	for _, entry := range entries {
		entry = strings.TrimPrefix(entry, "/")
		loaded.imported(entry)
		loaded.seen[a.manifest[entry].File] = true
	}
	w := newWalk(a)
	w.seen = loaded.seen
	for _, module := range modules {
		module = strings.TrimPrefix(module, "/")
		c, ok := a.manifest[module]
		if !ok {
			return "", fmt.Errorf("vite: %q is not in the manifest", module)
		}
		w.imported(module)
		w.add(&w.preload, c.File)
	}
	var b strings.Builder
	w.write(&b, a.base)
	return b.String(), nil
}

// walk collects the stylesheets and the imported chunks of manifest keys,
// each file once.
type walk struct {
	a            *Assets
	css, preload []string
	seen         map[string]bool
	visited      map[string]bool
}

func newWalk(a *Assets) *walk {
	return &walk{a: a, seen: map[string]bool{}, visited: map[string]bool{}}
}

func (w *walk) add(list *[]string, file string) {
	if file != "" && !w.seen[file] {
		w.seen[file] = true
		*list = append(*list, file)
	}
}

func (w *walk) imported(key string) {
	if w.visited[key] {
		return
	}
	w.visited[key] = true
	c := w.a.manifest[key]
	for _, file := range c.CSS {
		w.add(&w.css, file)
	}
	for _, imp := range c.Imports {
		w.imported(imp)
		w.add(&w.preload, w.a.manifest[imp].File)
	}
}

func (w *walk) write(b *strings.Builder, base string) {
	for _, file := range w.css {
		fmt.Fprintf(b, `<link rel="stylesheet" href="%s">`, html.EscapeString(base+file))
	}
	for _, file := range w.preload {
		fmt.Fprintf(b, `<link rel="modulepreload" href="%s">`, html.EscapeString(base+file))
	}
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
