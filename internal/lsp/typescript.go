package lsp

import (
	"github.com/elauven/vela/internal/typecheck"
	"github.com/elauven/vela/runtime"
)

// tsClient runs the TypeScript helper (internal/typecheck).
type tsClient = typecheck.Client

// velaEnv is what the helper declares besides the files: the ambient types
// of the runtime package (runtime/types, task 082) and the helpers of the
// render function of a virtual document.
var velaEnv = runtime.AmbientDeclarations() + typecheck.RenderHelpers

// virtualDoc is the virtual document of a component (internal/typecheck)
// with the component's lines, for locations.
type virtualDoc struct {
	*typecheck.Document
	path   string
	source *text
	text   string
}

func newVirtualDoc(path, src string) *virtualDoc {
	d := typecheck.Build(path, src)
	return &virtualDoc{Document: d, path: path, source: newText(src), text: d.Text}
}

func (v *virtualDoc) toVirtual(off int) (int, bool) { return v.ToVirtual(off) }
func (v *virtualDoc) toSource(off int) (int, bool)  { return v.ToSource(off) }
func (v *virtualDoc) toSourceRange(start, end int) (int, int, bool) {
	return v.ToSourceRange(start, end)
}

func utf16Index(s string, off int) int { return typecheck.UTF16Index(s, off) }
func byteOffset(s string, i int) int   { return typecheck.ByteOffset(s, i) }

// tsSpan is a location the helper returns: a UTF-16 span of file, with its
// line and character positions when TypeScript has the file.
type tsSpan struct {
	File         string `json:"file"`
	Start        int    `json:"start"`
	Length       int    `json:"length"`
	Line         *int   `json:"line"`
	Character    int    `json:"character"`
	EndLine      int    `json:"endLine"`
	EndCharacter int    `json:"endCharacter"`
	Prefix       string `json:"prefix"`
	Suffix       string `json:"suffix"`
}
