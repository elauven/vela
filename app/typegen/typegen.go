// Package typegen converts the Go types of loaders to TypeScript (docs/APP.md
// L-2, docs/TYPESCRIPT.md TS-8.2): the types a route component receives as
// its data and form props, as encoding/json writes them.
//
// `vela-app gen` of a TypeScript project builds a small program in the
// project's module that passes the loaders of its routes to Main, so that the
// types are read from the compiled packages: the Go loader stays the single
// source of truth. The rules follow encoding/json:
//
//   - struct fields by their json tag (`-` absent, `omitempty` optional,
//     the `string` option a string), unexported fields absent, embedded
//     structs flattened; a named struct is a named interface;
//   - bool boolean, numbers number, string string, time.Time string, []byte
//     string, other slices and arrays T[], maps with string, integer or text
//     keys Record<string, T>, pointers T | null, interfaces unknown;
//   - a type with its own MarshalJSON unknown, with MarshalText string;
//     functions and channels, which encoding/json cannot write, unknown.
package typegen

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Result is what Main writes: a TypeScript type for each key, written with
// the named types qualified by Qualifier, and the declarations of the named
// types.
type Result struct {
	Types        map[string]string `json:"types"`
	Declarations string            `json:"declarations"`
}

// Main writes the types of the loaders and form types as JSON to stdout:
// loaders maps a key to a loader func(ctx, r) (T, error), whose T is the
// key's type; forms maps a key to a type.
func Main(loaders map[string]any, forms map[string]reflect.Type) {
	if err := Write(os.Stdout, loaders, forms, "$data."); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Write is Main to w with the qualifier of named types.
func Write(w io.Writer, loaders map[string]any, forms map[string]reflect.Type, qualifier string) error {
	types := map[string]reflect.Type{}
	for key, loader := range loaders {
		t := reflect.TypeOf(loader)
		if t == nil || t.Kind() != reflect.Func || t.NumOut() != 2 {
			return fmt.Errorf("typegen: %s is not a loader", key)
		}
		types[key] = t.Out(0)
	}
	for key, t := range forms {
		types[key] = t
	}
	return json.NewEncoder(w).Encode(Convert(types, qualifier))
}

// Convert converts types; named types are declared once.
func Convert(types map[string]reflect.Type, qualifier string) Result {
	c := &converter{names: map[reflect.Type]string{}, taken: map[string]bool{}, decls: map[string]string{}}
	out := Result{Types: map[string]string{}}
	keys := make([]string, 0, len(types))
	for key := range types {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out.Types[key] = c.ts(types[key], qualifier)
	}
	names := make([]string, 0, len(c.decls))
	for name := range c.decls {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(c.decls[name])
	}
	out.Declarations = b.String()
	return out
}

type converter struct {
	names map[reflect.Type]string
	taken map[string]bool
	decls map[string]string
}

var (
	timeType          = reflect.TypeFor[time.Time]()
	marshalerType     = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(iface)
}

// ts is the TypeScript type of t; q qualifies named types.
func (c *converter) ts(t reflect.Type, q string) string {
	if t == nil {
		return "unknown"
	}
	switch {
	case t == timeType:
		return "string"
	case implements(t, marshalerType):
		return "unknown"
	case implements(t, textMarshalerType):
		return "string"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.String:
		return "string"
	case reflect.Interface:
		return "unknown"
	case reflect.Pointer:
		return c.ts(t.Elem(), q) + " | null"
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 && t.Kind() == reflect.Slice {
			return "string"
		}
		return element(c.ts(t.Elem(), q)) + "[]"
	case reflect.Map:
		switch k := t.Key(); {
		case k.Kind() == reflect.String, k.Kind() >= reflect.Int && k.Kind() <= reflect.Uintptr, implements(k, textMarshalerType):
			return "Record<string, " + c.ts(t.Elem(), q) + ">"
		}
		return "unknown"
	case reflect.Struct:
		if t.Name() == "" {
			return c.object(t, q, "")
		}
		return q + c.named(t)
	}
	return "unknown"
}

// element parenthesises a union for an array element type.
func element(s string) string {
	if strings.Contains(s, " | ") {
		return "(" + s + ")"
	}
	return s
}

// named declares the named struct t once and returns its name.
func (c *converter) named(t reflect.Type) string {
	if name, ok := c.names[t]; ok {
		return name
	}
	base := t.Name()
	if i := strings.IndexByte(base, '['); i >= 0 {
		base = base[:i] // a generic type: Page[T] is Page, Page2, …
	}
	name := base
	for n := 2; c.taken[name]; n++ {
		name = fmt.Sprintf("%s%d", base, n)
	}
	c.taken[name] = true
	c.names[t] = name
	c.decls[name] = "" // recursion: the name is known before the body
	comment := "/** " + t.PkgPath() + "." + t.Name() + " */\n"
	c.decls[name] = comment + "export interface " + name + " " + c.object(t, "", "") + "\n"
	return name
}

// field is one JSON member of a struct.
type field struct {
	name     string
	typ      string
	optional bool
	depth    int
	tagged   bool
}

// object is the object type of the struct t, members as encoding/json
// writes them.
func (c *converter) object(t reflect.Type, q, indent string) string {
	var fields []field
	c.fields(t, q, 0, map[reflect.Type]bool{}, &fields)
	// Dominance (encoding/json): the shallowest field of a name wins, a
	// tagged one over an untagged one at the same depth; ties drop both.
	byName := map[string][]field{}
	var order []string
	for _, f := range fields {
		if _, seen := byName[f.name]; !seen {
			order = append(order, f.name)
		}
		byName[f.name] = append(byName[f.name], f)
	}
	var members []string
	for _, name := range order {
		candidates := byName[name]
		best := candidates[0]
		tie := false
		for _, f := range candidates[1:] {
			switch {
			case f.depth < best.depth || f.depth == best.depth && f.tagged && !best.tagged:
				best, tie = f, false
			case f.depth == best.depth && f.tagged == best.tagged:
				tie = true
			}
		}
		if tie {
			continue
		}
		opt := ""
		if best.optional {
			opt = "?"
		}
		members = append(members, "  "+propertyName(best.name)+opt+": "+strings.ReplaceAll(best.typ, "\n", "\n  ")+";")
	}
	if len(members) == 0 {
		return "{}"
	}
	return "{\n" + strings.Join(members, "\n") + "\n}"
}

func (c *converter) fields(t reflect.Type, q string, depth int, visiting map[reflect.Type]bool, out *[]field) {
	if visiting[t] {
		return
	}
	visiting[t] = true
	defer delete(visiting, t)
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && !implements(ft, marshalerType) && !implements(ft, textMarshalerType) {
				c.fields(ft, q, depth+1, visiting, out)
				continue
			}
			if !f.IsExported() {
				continue
			}
		} else if !f.IsExported() {
			continue
		}
		tagged := name != ""
		if name == "" {
			name = f.Name
		}
		typ := c.ts(f.Type, q)
		if hasOption(opts, "string") {
			switch f.Type.Kind() {
			case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
				typ = "string"
			}
		}
		*out = append(*out, field{name: name, typ: typ, optional: hasOption(opts, "omitempty") || hasOption(opts, "omitzero"), depth: depth, tagged: tagged})
	}
}

func hasOption(opts, name string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == name {
			return true
		}
	}
	return false
}

func propertyName(name string) string {
	valid := name != ""
	for i, r := range name {
		if !(r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			valid = false
		}
	}
	if valid {
		return name
	}
	q, _ := json.Marshal(name)
	return string(q)
}
