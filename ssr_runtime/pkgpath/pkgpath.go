// Package pkgpath encodes source directory names into valid Go import path
// elements and back (docs/SSR.md "Build"). vela --ssr mirrors the source
// tree into one Go package per directory; the bracket and parenthesis names
// of file-based routers are not valid in Go import paths, so they are
// encoded:
//
//	[id]       id.param      package id_param
//	[[lang]]   lang.opt      package lang_opt
//	[...rest]  rest.rest     package rest_rest
//	[id=int]   id.int.param  package id_int_param
//	(admin)    admin.group   package admin_group
//
// Other names stay unchanged when they are valid. The encoding is
// reversible: the suffixes .param, .opt, .rest and .group are reserved for
// it. The compiler applies the same rules (internal/pkgpath, kept equal by
// a parity test); the app layer uses this package.
package pkgpath

import (
	"fmt"
	"go/token"
	"strings"
)

// Error explains why a directory name cannot be encoded.
type Error struct {
	Name    string // the source directory name
	Part    string // the offending part
	Reason  string
	Suggest string // a valid name, when one can be proposed
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("directory %q: %s", e.Name, e.Reason)
	if e.Part != "" && e.Part != e.Name {
		msg += fmt.Sprintf(" (%q)", e.Part)
	}
	msg += "; allowed: letters, digits, _, . and -, or one of [name], [[name]], [...name], [name=matcher], (name)"
	if e.Suggest != "" {
		msg += fmt.Sprintf("; for example %q", e.Suggest)
	}
	return msg
}

var reserved = []string{".param", ".opt", ".rest", ".group"}

// Encode returns the Go directory name of the source directory name.
func Encode(name string) (string, error) {
	fail := func(part, reason, suggest string) (string, error) {
		return "", &Error{Name: name, Part: part, Reason: reason, Suggest: suggest}
	}
	switch {
	case strings.HasPrefix(name, "[[") && strings.HasSuffix(name, "]]") && len(name) > 4:
		inner := name[2 : len(name)-2]
		if !identifier(inner) {
			return fail(inner, "an optional parameter name must be an identifier", "[["+suggestIdent(inner)+"]]")
		}
		return inner + ".opt", nil
	case strings.HasPrefix(name, "[...") && strings.HasSuffix(name, "]") && len(name) > 5:
		inner := name[4 : len(name)-1]
		if !identifier(inner) {
			return fail(inner, "a rest parameter name must be an identifier", "[..."+suggestIdent(inner)+"]")
		}
		return inner + ".rest", nil
	case strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") && len(name) > 2 && strings.Count(name, "[") == 1 && strings.Count(name, "]") == 1:
		inner := name[1 : len(name)-1]
		param, matcher, hasMatcher := strings.Cut(inner, "=")
		if !identifier(param) {
			return fail(param, "a parameter name must be an identifier", "["+suggestIdent(param)+"]")
		}
		if hasMatcher {
			if !identifier(matcher) {
				return fail(matcher, "a matcher name must be an identifier", "["+param+"="+suggestIdent(matcher)+"]")
			}
			return param + "." + matcher + ".param", nil
		}
		return param + ".param", nil
	case strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")") && len(name) > 2 && strings.Count(name, "(") == 1 && strings.Count(name, ")") == 1:
		inner := name[1 : len(name)-1]
		if !identifier(inner) {
			return fail(inner, "a group name must be an identifier", "("+suggestIdent(inner)+")")
		}
		return inner + ".group", nil
	}
	if strings.ContainsAny(name, "[]()") {
		return fail(name, "mixed segments are not supported; a bracket or parenthesis form must be the whole directory name", suggestPlain(name))
	}
	if name == "" || name == "." || name == ".." {
		return fail(name, "not a directory name", "")
	}
	for _, r := range name {
		if !plainRune(r) {
			return fail(string(r), "invalid character in a Go import path", suggestPlain(name))
		}
	}
	if name[0] == '-' || name[0] == '~' {
		return fail(name[:1], "a Go import path element cannot start with - or ~", suggestPlain(name))
	}
	if name[len(name)-1] == '.' {
		return fail(".", "a Go import path element cannot end with .", suggestPlain(name))
	}
	for _, suffix := range reserved {
		if strings.HasSuffix(name, suffix) {
			return fail(suffix, "reserved suffix of the encoding", suggestPlain(strings.TrimSuffix(name, suffix)))
		}
	}
	return name, nil
}

// Decode returns the source directory name of a Go directory name that
// Encode produced.
func Decode(encoded string) (string, error) {
	base, suffix := encoded, ""
	for _, s := range reserved {
		if strings.HasSuffix(encoded, s) {
			base, suffix = strings.TrimSuffix(encoded, s), s
			break
		}
	}
	var name string
	switch suffix {
	case "":
		name = encoded
	case ".param":
		if param, matcher, ok := strings.Cut(base, "."); ok {
			name = "[" + param + "=" + matcher + "]"
		} else {
			name = "[" + base + "]"
		}
	case ".opt":
		name = "[[" + base + "]]"
	case ".rest":
		name = "[..." + base + "]"
	case ".group":
		name = "(" + base + ")"
	}
	if again, err := Encode(name); err != nil || again != encoded {
		return "", fmt.Errorf("%q is not an encoded directory name", encoded)
	}
	return name, nil
}

// EncodePath encodes every element of a slash-separated relative path.
func EncodePath(rel string) (string, error) {
	if rel == "" || rel == "." {
		return rel, nil
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		encoded, err := Encode(part)
		if err != nil {
			return "", err
		}
		parts[i] = encoded
	}
	return strings.Join(parts, "/"), nil
}

// DecodePath decodes every element of a slash-separated relative path.
func DecodePath(rel string) (string, error) {
	if rel == "" || rel == "." {
		return rel, nil
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		decoded, err := Decode(part)
		if err != nil {
			return "", err
		}
		parts[i] = decoded
	}
	return strings.Join(parts, "/"), nil
}

// PackageName is the Go package name of an encoded directory name: . - and ~
// become _, a leading digit gets a p prefix, and a Go keyword a _ suffix.
func PackageName(encoded string) string {
	var b strings.Builder
	for _, r := range encoded {
		switch {
		case r == '.' || r == '-' || r == '~':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" {
		return "p"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "p" + name
	}
	if token.Lookup(name).IsKeyword() {
		name += "_"
	}
	return name
}

func identifier(s string) bool {
	if s == "" || !letter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !letter(s[i]) && !digit(s[i]) && s[i] != '_' {
			return false
		}
	}
	return true
}

func letter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func digit(c byte) bool  { return c >= '0' && c <= '9' }

func plainRune(r rune) bool {
	return r < 0x80 && (letter(byte(r)) || digit(byte(r)) || r == '_' || r == '.' || r == '-' || r == '~')
}

// suggest turns a name into a valid plain one: invalid characters become
// -, a leading digit or separator is dropped.
func suggest(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range name {
		if r < 0x80 && (letter(byte(r)) || digit(byte(r)) || r == '_') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// suggestPlain is suggest with a fallback for a name without usable
// characters.
func suggestPlain(name string) string {
	if out := suggest(name); out != "" {
		return out
	}
	return "dir"
}

// suggestIdent turns a name into an identifier for the bracket forms.
func suggestIdent(name string) string {
	out := strings.ReplaceAll(suggest(name), "-", "_")
	for out != "" && !letter(out[0]) {
		out = out[1:]
	}
	if out == "" {
		return "name"
	}
	return out
}
