package main

import (
	"reflect"
	"strconv"
	"strings"
)

// astjson.go — serializes a generated JsProgram to a JSON tree for
// inspection.  This is debugging infrastructure for the Stage 4
// rewrite: a JSON dump plus the HTML viewer turns "trees diverge at
// signature offset 2640" into something a human can navigate.
//
// The serializer is reflection-based.  With 50-plus node struct
// types, a hand-written MarshalJSON per type would be tedious and
// would silently miss any future node type.  Reflection walks
// whatever node it is handed: it emits the concrete type name,
// then every exported field — recursing into JsNode-typed fields
// and slices, formatting scalars inline.
//
// Position fields (Pos/End/Origin, embedded via baseNode) are
// omitted by default; they are emission bookkeeping, not tree
// structure, and including them buries the signal.  The withPos
// argument re-includes them when a position-level question is
// being investigated.

// ASTToJSON renders a JsProgram as an indented JSON string.  When
// withPos is true, Pos/End/Origin bookkeeping fields are included.
func ASTToJSON(prog *JsProgram, withPos bool) string {
	var b strings.Builder
	enc := &astEncoder{out: &b, withPos: withPos}
	enc.encode(reflect.ValueOf(prog), 0)
	b.WriteByte('\n')
	return b.String()
}

type astEncoder struct {
	out     *strings.Builder
	withPos bool
}

// posFields are the embedded bookkeeping fields suppressed unless
// withPos is set.
var posFields = map[string]bool{
	"Pos": true, "End": true, "Origin": true,
}

func (e *astEncoder) indent(depth int) {
	for i := 0; i < depth; i++ {
		e.out.WriteString("  ")
	}
}

// encode writes the JSON representation of an arbitrary reflect
// value at the given indentation depth.
func (e *astEncoder) encode(v reflect.Value, depth int) {
	// Unwrap interfaces and pointers.
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			e.out.WriteString("null")
			return
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Struct:
		e.encodeStruct(v, depth)
	case reflect.Slice, reflect.Array:
		e.encodeSlice(v, depth)
	case reflect.String:
		e.out.WriteString(jsonQuote(v.String()))
	case reflect.Bool:
		if v.Bool() {
			e.out.WriteString("true")
		} else {
			e.out.WriteString("false")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.out.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.out.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		e.out.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	default:
		// Anything exotic — render its Go string form, quoted.
		e.out.WriteString(jsonQuote(v.String()))
	}
}

// encodeStruct writes a node struct: a "_type" key with the
// concrete type name, then every exported field.  Embedded base
// structs are flattened so Pos/End surface (or are skipped) at the
// node's own level.
func (e *astEncoder) encodeStruct(v reflect.Value, depth int) {
	t := v.Type()
	e.out.WriteString("{\n")
	e.indent(depth + 1)
	e.out.WriteString(`"_type": `)
	e.out.WriteString(jsonQuote(t.Name()))

	e.encodeFields(v, depth, true)

	e.out.WriteByte('\n')
	e.indent(depth)
	e.out.WriteByte('}')
}

// encodeFields writes the exported fields of a struct value.  It
// recurses through embedded (anonymous) structs so a node's
// inherited fields appear at the node's own level.  hadPrev tracks
// whether a comma is needed before the next field.
func (e *astEncoder) encodeFields(v reflect.Value, depth int, hadPrev bool) bool {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)

		// Embedded base structs (baseNode, baseStmt, baseExpr) —
		// flatten their fields into this node.
		if f.Anonymous {
			hadPrev = e.encodeFields(fv, depth, hadPrev)
			continue
		}
		// Unexported fields are not part of the tree.
		if f.PkgPath != "" {
			continue
		}
		// Position bookkeeping, suppressed unless requested.
		if posFields[f.Name] && !e.withPos {
			continue
		}

		if hadPrev {
			e.out.WriteByte(',')
		}
		e.out.WriteByte('\n')
		e.indent(depth + 1)
		e.out.WriteString(jsonQuote(f.Name))
		e.out.WriteString(": ")
		e.encode(fv, depth+1)
		hadPrev = true
	}
	return hadPrev
}

// encodeSlice writes a JSON array, one element per line.
func (e *astEncoder) encodeSlice(v reflect.Value, depth int) {
	n := v.Len()
	if n == 0 {
		e.out.WriteString("[]")
		return
	}
	e.out.WriteString("[\n")
	for i := 0; i < n; i++ {
		e.indent(depth + 1)
		e.encode(v.Index(i), depth+1)
		if i < n-1 {
			e.out.WriteByte(',')
		}
		e.out.WriteByte('\n')
	}
	e.indent(depth)
	e.out.WriteByte(']')
}

// jsonQuote returns a JSON-quoted string literal.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				b.WriteString(`\u`)
				b.WriteByte(hex[(r>>12)&0xf])
				b.WriteByte(hex[(r>>8)&0xf])
				b.WriteByte(hex[(r>>4)&0xf])
				b.WriteByte(hex[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
