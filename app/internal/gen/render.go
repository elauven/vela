package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Rendering rules of the pages (task 092): Prerender, Cache and
// `const Render = app.RenderClient` of load.go. A RenderClient page has no
// Go renderer: the generator gives its Page.vela <script context="client">
// (T-9.2a), the way gofmt -w rewrites a file, and the compiler learns
// nothing about routes (B-2).

// Prerenders reports a route that declares Prerender.
func (m *Model) Prerenders() bool {
	for _, r := range m.Routes {
		if r.PrerenderPkg != "" {
			return true
		}
	}
	return false
}

// checkRender reports the rules of a route that do not go together.
func (m *Model) checkRender(r *Route) []Diagnostic {
	var diags []Diagnostic
	load := r.Dir.Load
	if load == nil {
		return nil
	}
	at := func(pos Diagnostic, message, help string) {
		pos.Code, pos.Message, pos.Help = CodeRender, message, help
		diags = append(diags, pos)
	}
	if load.Prerender {
		switch {
		case r.RenderMode == "client":
			at(load.PrerenderPos, fmt.Sprintf("route %s is prerendered and rendered only in the browser (RenderClient)", r.ID), "remove Prerender or const Render = app.RenderClient")
		case r.ActionsPkg != "":
			at(load.PrerenderPos, fmt.Sprintf("route %s is prerendered and has actions; a prerendered page is a file and cannot answer a form", r.ID), "remove Prerender, or move the actions to a page that is not prerendered")
		case load.Cache:
			at(load.CachePos, fmt.Sprintf("route %s is prerendered and cached; a prerendered page is a file, Cache does not apply", r.ID), "remove Cache or Prerender")
		}
		for _, l := range r.Chain {
			if l.GuardPkg != "" {
				at(load.PrerenderPos, fmt.Sprintf("route %s is prerendered below the guard of %s; the files would be served without running it", r.ID, l.Dir.Rel), "remove Prerender, or move the page out of the guarded directory")
				break
			}
		}
	}
	return diags
}

var (
	scriptOpen    = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	scriptContext = regexp.MustCompile(`(?i)\scontext\s*=\s*["']?client["']?`)
)

// componentScript finds the opening tag of a component's own <script>: the
// first one outside the <template> section.
func componentScript(src string) (start, end int, ok bool) {
	low := strings.ToLower(src)
	tStart := strings.Index(low, "<template")
	tEnd := -1
	if tStart >= 0 {
		if i := strings.Index(low[tStart:], "</template>"); i >= 0 {
			tEnd = tStart + i
		}
	}
	for _, loc := range scriptOpen.FindAllStringIndex(src, -1) {
		if tStart >= 0 && loc[0] > tStart && (tEnd < 0 || loc[0] < tEnd) {
			continue
		}
		return loc[0], loc[1], true
	}
	return 0, 0, false
}

// clientOnly reports <script context="client"> in a component source.
func clientOnly(src string) bool {
	start, end, ok := componentScript(src)
	return ok && scriptContext.MatchString(src[start:end])
}

// makeClientOnly adds context="client" to the component's <script>, or an
// empty <script context="client"></script> before everything else.
func makeClientOnly(src string) string {
	if start, end, ok := componentScript(src); ok {
		insert := start + len("<script")
		_ = end
		return src[:insert] + ` context="client"` + src[insert:]
	}
	return "<script context=\"client\"></script>\n\n" + src
}

// clientEdit is a pending edit of a RenderClient page.
type clientEdit struct {
	file, rel, src string
}

// planClientPages marks the client-only pages and returns the edits that
// give the RenderClient pages <script context="client">; it warns about a
// client-only page without RenderClient, whose attribute it leaves alone.
func (m *Model) planClientPages() (edits []clientEdit, notes []string) {
	for _, r := range m.Routes {
		file := filepath.Join(m.Config.Root, filepath.FromSlash(r.Dir.Page))
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		src := string(data)
		switch {
		case r.RenderMode == "client":
			r.ClientOnly = true
			if !clientOnly(src) {
				edits = append(edits, clientEdit{file: file, rel: r.Dir.Page, src: makeClientOnly(src)})
			}
		case clientOnly(src):
			r.ClientOnly = true
			notes = append(notes, fmt.Sprintf("%s: [warning] [%s] the page has <script context=\"client\"> but its load.go does not declare const Render = app.RenderClient; the document renders it like a {#client} block", r.Dir.Page, CodeRender))
		}
	}
	return edits, notes
}

// applyClientEdits writes the edits and returns the changed files.
func applyClientEdits(edits []clientEdit) ([]string, error) {
	var edited []string
	for _, e := range edits {
		if err := os.WriteFile(e.file, []byte(e.src), 0o644); err != nil {
			return edited, err
		}
		edited = append(edited, e.rel)
	}
	return edited, nil
}
