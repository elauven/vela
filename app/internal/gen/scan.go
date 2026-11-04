package gen

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elauven/vela/ssr_runtime/pkgpath"
)

// SegmentKind is the kind of a routes directory (docs/APP.md P-2).
type SegmentKind int

const (
	SegStatic   SegmentKind = iota // users
	SegParam                       // [id], [id=int]
	SegOptional                    // [[lang]]
	SegRest                        // [...path]
	SegGroup                       // (admin)
)

// DirSegment is the meaning of one directory name.
type DirSegment struct {
	Kind SegmentKind
	// Name is the static text, the parameter name or the group name.
	Name    string
	Matcher string
}

// ParseSegment parses a routes directory name. Names pkgpath cannot encode
// are rejected with its explanation (P-3), so the routes tree and the
// mirrored Go tree cannot drift.
func ParseSegment(name string) (DirSegment, error) {
	if _, err := pkgpath.Encode(name); err != nil {
		return DirSegment{}, err
	}
	switch {
	case strings.HasPrefix(name, "[["):
		return DirSegment{Kind: SegOptional, Name: name[2 : len(name)-2]}, nil
	case strings.HasPrefix(name, "[..."):
		return DirSegment{Kind: SegRest, Name: name[4 : len(name)-1]}, nil
	case strings.HasPrefix(name, "["):
		param, matcher, _ := strings.Cut(name[1:len(name)-1], "=")
		return DirSegment{Kind: SegParam, Name: param, Matcher: matcher}, nil
	case strings.HasPrefix(name, "("):
		return DirSegment{Kind: SegGroup, Name: name[1 : len(name)-1]}, nil
	}
	return DirSegment{Kind: SegStatic, Name: name}, nil
}

// Dir is one directory of the routes tree.
type Dir struct {
	// Rel is the path relative to the routes directory ("" for the root).
	Rel     string
	Segment DirSegment // zero for the root
	// GoRel is Rel encoded with pkgpath ("." for the root).
	GoRel    string
	Parent   *Dir
	Children []*Dir
	// Page, Layout and Error are project-relative paths of the components,
	// "" when absent.
	Page, Layout, Error string
	// Load is what the mirrored Go directory declares (nil without Go files).
	Load *LoadFile
}

// Tree is a scanned project.
type Tree struct {
	Config   Config
	Root     *Dir
	Dirs     []*Dir // in walk order: parents before children
	byRel    map[string]*Dir
	Matchers map[string]*CustomMatcher
}

// projectRel is a routes-relative path as a project-relative one.
func (t *Tree) projectRel(rel string) string {
	if rel == "" {
		return t.Config.Routes
	}
	return t.Config.Routes + "/" + rel
}

// position is the file a directory diagnostic points at: its Page.vela or
// Layout.vela when it has one, so editors can open it, else the directory.
func (t *Tree) position(d *Dir) string {
	for _, file := range []string{d.Page, d.Layout, d.Error} {
		if file != "" {
			return file
		}
	}
	return t.projectRel(d.Rel) + "/"
}

// Scan reads the routes tree, the mirrored Go tree and the matchers.
func Scan(cfg Config) (*Tree, []Diagnostic) {
	t := &Tree{Config: cfg, byRel: map[string]*Dir{}}
	var diags []Diagnostic
	report := func(d Diagnostic) { diags = append(diags, d) }
	rootPath := cfg.abs(cfg.Routes)
	if info, err := os.Stat(rootPath); err != nil || !info.IsDir() {
		report(Diagnostic{File: cfg.Routes + "/", Code: CodeConfig, Message: "the routes directory does not exist", Help: fmt.Sprintf("create %s/Page.vela, or set \"routes\" in %s", cfg.Routes, ConfigFile)})
		return t, diags
	}
	t.Root = &Dir{GoRel: "."}
	t.byRel[""] = t.Root
	t.walk(t.Root, report)
	t.checkPaths(report)
	t.scanServer(report)
	t.scanMatchers(report)
	sortDiagnostics(diags)
	return t, diags
}

func (t *Tree) walk(d *Dir, report func(Diagnostic)) {
	t.Dirs = append(t.Dirs, d)
	entries, err := os.ReadDir(t.Config.abs(t.projectRel(d.Rel)))
	if err != nil {
		report(Diagnostic{File: t.projectRel(d.Rel) + "/", Code: CodeConfig, Message: err.Error()})
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		rel := path.Join(d.Rel, name)
		file := t.projectRel(rel)
		if entry.IsDir() {
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			segment, err := ParseSegment(name)
			if err != nil {
				help := ""
				if perr, ok := err.(*pkgpath.Error); ok && perr.Suggest != "" {
					help = fmt.Sprintf("rename the directory, for example to %q", perr.Suggest)
				}
				report(Diagnostic{File: file + "/", Code: CodeDirectory, Message: err.Error(), Help: help})
				continue
			}
			goName, _ := pkgpath.Encode(name)
			child := &Dir{Rel: rel, Segment: segment, GoRel: path.Join(d.GoRel, goName), Parent: d}
			d.Children = append(d.Children, child)
			t.byRel[rel] = child
			t.walk(child, report)
			continue
		}
		switch ext := path.Ext(name); {
		case name == "Page.vela":
			d.Page = file
		case name == "Layout.vela":
			d.Layout = file
		case name == "Error.vela":
			d.Error = file
		case ext == ".vela":
			report(Diagnostic{File: file, Line: 1, Col: 1, Code: CodeFile, Message: "only Page.vela, Layout.vela and Error.vela belong in the routes directory (P-1)", Help: "move shared components outside " + t.Config.Routes})
		case ext == ".go":
			mirror := path.Join(t.Config.Server, d.GoRel, name)
			report(Diagnostic{File: file, Line: 1, Col: 1, Code: CodeFile, Message: "a Go file in the routes directory breaks go build ./... of the module (P-17)", Help: "move it to the mirrored Go tree: " + mirror})
		}
	}
}

// checkPaths applies the rules along each path: rest last (P-4) and unique
// parameter names (P-5).
func (t *Tree) checkPaths(report func(Diagnostic)) {
	for _, d := range t.Dirs {
		if d == t.Root {
			continue
		}
		for up := d.Parent; up != nil; up = up.Parent {
			if up.Segment.Kind == SegRest && d.Segment.Kind != SegGroup {
				report(Diagnostic{File: t.projectRel(d.Rel) + "/", Code: CodeRest, Message: fmt.Sprintf("%s must be the last directory of a path, but %s is below it (P-4)", path.Base(up.Rel), path.Base(d.Rel)), Help: "move the routes below it out of " + t.projectRel(up.Rel)})
				break
			}
		}
		if !d.Segment.isParam() {
			continue
		}
		for up := d.Parent; up != nil; up = up.Parent {
			if up.Segment.isParam() && up.Segment.Name == d.Segment.Name {
				report(Diagnostic{File: t.projectRel(d.Rel) + "/", Code: CodeParam, Message: fmt.Sprintf("parameter %q is already declared by %s (P-5)", d.Segment.Name, t.projectRel(up.Rel)), Help: "give the parameters of one path different names"})
				break
			}
		}
	}
}

func (s DirSegment) isParam() bool {
	return s.Kind == SegParam || s.Kind == SegOptional || s.Kind == SegRest
}

// scanServer reads the mirrored Go tree (P-16, P-17): the loaders of each
// route directory and the directories that mirror nothing.
func (t *Tree) scanServer(report func(Diagnostic)) {
	root := t.Config.abs(t.Config.Server)
	if _, err := os.Stat(root); err != nil {
		return
	}
	_ = filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, file)
		rel = filepath.ToSlash(rel)
		goFiles := goFilesIn(file)
		if len(goFiles) == 0 {
			return nil
		}
		dirFile := path.Join(t.Config.Server, rel) + "/"
		routeRel, err := pkgpath.DecodePath(rel)
		if rel == "." {
			routeRel = ""
		}
		d := t.byRel[routeRel]
		if err != nil || d == nil {
			help := "remove it, or move its files to the directory that mirrors their route"
			if err == nil {
				help = fmt.Sprintf("it mirrors %s, which does not exist; %s", t.projectRel(routeRel), help)
			}
			report(Diagnostic{File: dirFile, Code: CodeStale, Message: "a mirrored Go directory without a route directory (a stale loader, P-17)", Help: help})
			return nil
		}
		d.Load = t.readLoad(d, file, goFiles, report)
		return nil
	})
}

func goFilesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	return files
}
