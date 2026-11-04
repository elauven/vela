package gen

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/elauven/vela/app"
)

// Model is the route table the generators write (docs/APP.md P-8–P-12).
type Model struct {
	Config Config
	Routes []*Route
	// Patterns are in ranking order: the first full match wins.
	Patterns []*Pattern
	// Components are the route components by segment id.
	Components []*Component
	// Matchers are the custom matchers, by name.
	Matchers []*CustomMatcher
	// Error is the segment id of the root Error.vela, "" without one.
	Error string
	// NotFound are the levels of a URL no route matches: the layout of the
	// routes directory itself, around the root Error.vela.
	NotFound []*Level
	// Depth is the maximum chain length.
	Depth int
}

// Route is a directory with a Page.vela.
type Route struct {
	ID    string // P-12
	Dir   *Dir
	Chain []*Level
	// RenderPkg is the module-relative package that declares Render, "".
	RenderPkg string
}

// Level is one level of a chain (P-10).
type Level struct {
	ID    string // segment id (P-11)
	Kind  ComponentKind
	Dir   *Dir
	Error string // nearest Error.vela at or above Dir (R-3)
	// LoaderPkg is the module-relative package of the loader, "" for none.
	LoaderPkg string
	// DepsPkg is the package that declares Deps, "" for none.
	DepsPkg string
	Params  []string // parameters of the level's own path (L-6)
	// Head: the loader may set the head (LoadFile.PageHead, LayoutHead).
	Head bool
}

// Pattern is one URL pattern of a route; a route with optional segments has
// one per combination of them.
type Pattern struct {
	Route    int
	Segments []app.Segment
	ranks    []int
	dirs     []*Dir // the directory of each segment
	optional []bool // the segment comes from an optional directory
	skipped  bool   // an optional directory was left out
}

// ComponentKind is the role of a route component.
type ComponentKind int

const (
	KindLayout ComponentKind = iota
	KindPage
	KindError
)

func (k ComponentKind) String() string {
	return [...]string{"Layout", "Page", "Error"}[k]
}

// Component is a route component.
type Component struct {
	ID   string
	File string // project-relative
	Kind ComponentKind
	// Local is the name VelaApp.vela gives it.
	Local string
}

// Ranks of a pattern segment (P-8). The end of a pattern ranks first, so
// /a is tried before /a/[...rest].
const (
	rankEnd = iota
	rankStatic
	rankMatcher
	rankParam
	rankOptional
	rankRest
)

// Build turns a scanned tree into the model and checks the rules that need
// the whole table: ranks (P-8) and duplicates (P-9).
func Build(t *Tree) (*Model, []Diagnostic) {
	m := &Model{Config: t.Config}
	var diags []Diagnostic
	components := map[string]*Component{}
	component := func(file string, kind ComponentKind) string {
		id := t.Config.componentID(file)
		if components[id] == nil {
			components[id] = &Component{ID: id, File: file, Kind: kind}
		}
		return id
	}
	nearestError := func(d *Dir) string {
		for ; d != nil; d = d.Parent {
			if d.Error != "" {
				return component(d.Error, KindError)
			}
		}
		return ""
	}
	if t.Root == nil {
		return m, nil
	}
	m.Error = nearestError(t.Root)
	newLevel := func(dir *Dir, kind ComponentKind, file string, loader bool, params []string) *Level {
		l := &Level{ID: component(file, kind), Kind: kind, Dir: dir, Error: nearestError(dir), Params: append([]string{}, params...)}
		if dir.Load != nil {
			if loader {
				l.LoaderPkg = dir.Load.Dir
				l.Head = kind == KindPage && dir.Load.PageHead || kind == KindLayout && dir.Load.LayoutHead
			}
			if dir.Load.Deps {
				l.DepsPkg = dir.Load.Dir
			}
		}
		return l
	}
	if t.Root.Layout != "" {
		m.NotFound = []*Level{newLevel(t.Root, KindLayout, t.Root.Layout, t.Root.Load != nil && t.Root.Load.Layout, nil)}
	}
	for _, d := range t.Dirs {
		if d.Error != "" {
			component(d.Error, KindError)
		}
		if d.Page == "" {
			continue
		}
		r := &Route{ID: routeID(d), Dir: d}
		var dirs []*Dir
		for up := d; up != nil; up = up.Parent {
			dirs = append([]*Dir{up}, dirs...)
		}
		var params []string
		for _, dir := range dirs {
			if dir.Segment.isParam() && dir != t.Root {
				params = append(params, dir.Segment.Name)
			}
			level := func(kind ComponentKind, file string, loader bool) *Level {
				return newLevel(dir, kind, file, loader, params)
			}
			if dir.Layout != "" {
				r.Chain = append(r.Chain, level(KindLayout, dir.Layout, dir.Load != nil && dir.Load.Layout))
			}
			if dir == d {
				r.Chain = append(r.Chain, level(KindPage, dir.Page, dir.Load != nil && dir.Load.Page))
				if dir.Load != nil && dir.Load.Render {
					r.RenderPkg = dir.Load.Dir
				}
			}
		}
		m.Depth = max(m.Depth, len(r.Chain))
		m.Routes = append(m.Routes, r)
	}
	sort.SliceStable(m.Routes, func(i, j int) bool { return m.Routes[i].ID < m.Routes[j].ID })
	for i, r := range m.Routes {
		for _, p := range expand(t, r.Dir) {
			p.Route = i
			m.Patterns = append(m.Patterns, p)
		}
	}
	sort.SliceStable(m.Patterns, func(i, j int) bool { return lessPattern(m.Patterns[i], m.Patterns[j]) })
	diags = append(diags, m.checkPatterns(t)...)
	for _, c := range components {
		m.Components = append(m.Components, c)
	}
	sort.Slice(m.Components, func(i, j int) bool { return m.Components[i].ID < m.Components[j].ID })
	counts := map[ComponentKind]int{}
	for _, c := range m.Components {
		c.Local = fmt.Sprintf("%s%d", c.Kind, counts[c.Kind])
		counts[c.Kind]++
	}
	for _, name := range sortedKeys(t.Matchers) {
		m.Matchers = append(m.Matchers, t.Matchers[name])
	}
	sortDiagnostics(diags)
	return m, diags
}

// routeID is the directory path with groups and brackets kept (P-12).
func routeID(d *Dir) string {
	return "/" + d.Rel
}

// expand lists the patterns of the route at d: an optional directory is
// tried with a value first and then without one (P-8), and a group adds no
// segment.
func expand(t *Tree, d *Dir) []*Pattern {
	var dirs []*Dir
	for up := d; up != nil && up != t.Root; up = up.Parent {
		dirs = append([]*Dir{up}, dirs...)
	}
	patterns := []*Pattern{{}}
	for _, dir := range dirs {
		s := dir.Segment
		var next []*Pattern
		for _, p := range patterns {
			add := func(segment app.Segment, rank int, optional bool) *Pattern {
				q := &Pattern{
					Segments: append(append([]app.Segment{}, p.Segments...), segment),
					ranks:    append(append([]int{}, p.ranks...), rank),
					dirs:     append(append([]*Dir{}, p.dirs...), dir),
					optional: append(append([]bool{}, p.optional...), optional),
					skipped:  p.skipped,
				}
				return q
			}
			switch s.Kind {
			case SegGroup:
				next = append(next, p)
			case SegStatic:
				next = append(next, add(app.Segment{Kind: app.Static, Value: s.Name}, rankStatic, false))
			case SegParam:
				rank := rankParam
				if s.Matcher != "" {
					rank = rankMatcher
				}
				next = append(next, add(app.Segment{Kind: app.Param, Value: s.Name, Matcher: s.Matcher}, rank, false))
			case SegOptional:
				without := *p
				without.skipped = true
				next = append(next, add(app.Segment{Kind: app.Param, Value: s.Name}, rankOptional, true), &without)
			case SegRest:
				next = append(next, add(app.Segment{Kind: app.Rest, Value: s.Name}, rankRest, false))
			}
		}
		patterns = next
	}
	// Two combinations of optional directories can give the same shape
	// ([[a]]/[[b]] without b, and without a); the first one wins.
	seen := map[string]bool{}
	var out []*Pattern
	for _, p := range patterns {
		if key := p.key(); !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	return out
}

// key is the URL shape of a pattern: parameter names do not take part.
func (p *Pattern) key() string {
	var b strings.Builder
	for i, s := range p.Segments {
		switch s.Kind {
		case app.Static:
			b.WriteString("/" + s.Value)
		case app.Param:
			fmt.Fprintf(&b, "/[%d=%s]", p.ranks[i], s.Matcher)
		case app.Rest:
			b.WriteString("/[...]")
		}
	}
	return b.String()
}

func (p *Pattern) rank(i int) int {
	if i >= len(p.ranks) {
		return rankEnd
	}
	return p.ranks[i]
}

// lessPattern orders patterns segment by segment from the left: static,
// parameter with a matcher, parameter, optional, rest (P-8). Patterns of
// one rank keep the order of their route ids; different matchers of one
// level are tried in name order.
func lessPattern(a, b *Pattern) bool {
	for i := 0; i < max(len(a.ranks), len(b.ranks)); i++ {
		ra, rb := a.rank(i), b.rank(i)
		if ra != rb {
			return ra < rb
		}
		if ra == rankMatcher && a.Segments[i].Matcher != b.Segments[i].Matcher {
			return a.Segments[i].Matcher < b.Segments[i].Matcher
		}
	}
	return false
}

// checkPatterns reports two routes with the same URL pattern (P-9) and two
// candidates of the same rank at one level (P-8).
func (m *Model) checkPatterns(t *Tree) []Diagnostic {
	var diags []Diagnostic
	reported := map[[2]int]bool{}
	for i, a := range m.Patterns {
		for _, b := range m.Patterns[i+1:] {
			if a.Route == b.Route {
				continue
			}
			pair := [2]int{min(a.Route, b.Route), max(a.Route, b.Route)}
			if reported[pair] {
				continue
			}
			first, second := m.Routes[pair[0]], m.Routes[pair[1]]
			if a.key() == b.key() {
				reported[pair] = true
				via := ""
				if a.fromOptional() || b.fromOptional() {
					via = " through an optional segment"
				} else if strings.Contains(first.ID, "(") || strings.Contains(second.ID, "(") {
					via = " through a group"
				}
				diags = append(diags, Diagnostic{File: t.position(second.Dir), Line: 1, Col: 1, Code: CodeDuplicate, Message: fmt.Sprintf("routes %s and %s have the same URL pattern %s%s (P-9)", first.ID, second.ID, displayKey(a), via), Help: "remove or rename one of the routes"})
				continue
			}
			if level, ok := sameRank(a, b); ok {
				reported[pair] = true
				later := b
				if b.Route != pair[1] {
					later = a
				}
				dir := later.dirs[level]
				earlier := a
				if later == a {
					earlier = b
				}
				diags = append(diags, Diagnostic{File: t.projectRel(dir.Rel) + "/", Code: CodeRank, Message: fmt.Sprintf("%s and %s are candidates of the same rank at one level (P-8)", path.Base(earlier.dirs[level].Rel), path.Base(dir.Rel)), Help: "give one of them a matcher, or a static segment"})
			}
		}
	}
	return diags
}

// sameRank finds the first level where the patterns differ and reports
// whether they differ only in the parameter name of one rank there.
func sameRank(a, b *Pattern) (int, bool) {
	for i := 0; i < min(len(a.Segments), len(b.Segments)); i++ {
		sa, sb := a.Segments[i], b.Segments[i]
		if sa == sb && a.ranks[i] == b.ranks[i] {
			continue
		}
		return i, sa.Kind != app.Static && sa.Kind == sb.Kind && a.ranks[i] == b.ranks[i] && sa.Matcher == sb.Matcher
	}
	return 0, false
}

func (p *Pattern) fromOptional() bool {
	if p.skipped {
		return true
	}
	for _, o := range p.optional {
		if o {
			return true
		}
	}
	return false
}

// displayKey shows a pattern as a URL with parameters in brackets.
func displayKey(p *Pattern) string {
	if len(p.Segments) == 0 {
		return "/"
	}
	var b strings.Builder
	for _, s := range p.Segments {
		switch s.Kind {
		case app.Static:
			b.WriteString("/" + s.Value)
		case app.Param:
			if s.Matcher != "" {
				b.WriteString("/[" + s.Value + "=" + s.Matcher + "]")
			} else {
				b.WriteString("/[" + s.Value + "]")
			}
		case app.Rest:
			b.WriteString("/[..." + s.Value + "]")
		}
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Tables is the route table as app.Routes, for the tests that match URLs
// with the model the generators write.
func (m *Model) Tables(matchers map[string]func(string) bool) app.Routes {
	table := app.Routes{Matchers: matchers, Error: m.Error, Depth: m.Depth}
	for _, r := range m.Routes {
		route := app.Route{ID: r.ID}
		for _, l := range r.Chain {
			route.Chain = append(route.Chain, app.Level{ID: l.ID, Error: l.Error, Params: l.Params})
		}
		table.Routes = append(table.Routes, route)
	}
	for _, p := range m.Patterns {
		table.Patterns = append(table.Patterns, app.Pattern{Route: p.Route, Segments: p.Segments})
	}
	return table
}
