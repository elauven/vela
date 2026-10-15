package ssr_runtime

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// appendJSON appends the JSON text of value exactly as encoding/json writes
// it (HTML-safe string escaping, ECMAScript number format, sorted map keys),
// in one pass. Encoding *Object through json.Marshal was quadratic in the
// nesting depth: every level re-validated and re-formatted the MarshalJSON
// output of the levels below it, which made the hydration payload the main
// cost of rendering large lists (task 058).
func appendJSON(b []byte, value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return append(b, "null"...), nil
	case bool:
		if v {
			return append(b, "true"...), nil
		}
		return append(b, "false"...), nil
	case string:
		return appendJSONString(b, v), nil
	case float64:
		return appendJSONFloat(b, v)
	case int:
		return strconv.AppendInt(b, int64(v), 10), nil
	case int64:
		return strconv.AppendInt(b, v, 10), nil
	case int32:
		return strconv.AppendInt(b, int64(v), 10), nil
	case int16:
		return strconv.AppendInt(b, int64(v), 10), nil
	case int8:
		return strconv.AppendInt(b, int64(v), 10), nil
	case uint:
		return strconv.AppendUint(b, uint64(v), 10), nil
	case uint64:
		return strconv.AppendUint(b, v, 10), nil
	case uint32:
		return strconv.AppendUint(b, uint64(v), 10), nil
	case uint16:
		return strconv.AppendUint(b, uint64(v), 10), nil
	case UndefinedValue:
		return append(b, "null"...), nil
	case []any:
		if v == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '[')
		for i, item := range v {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			if b, err = appendJSON(b, item); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case *Object:
		if v == nil {
			return append(b, "null"...), nil
		}
		b = append(b, '{')
		keys, vals := v.entries()
		for i, key := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, key)
			b = append(b, ':')
			var err error
			if b, err = appendJSON(b, vals[i]); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	case map[string]any:
		if v == nil {
			return append(b, "null"...), nil
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b = append(b, '{')
		for i, key := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, key)
			b = append(b, ':')
			var err error
			if b, err = appendJSON(b, v[key]); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	case json.RawMessage:
		return appendCompactJSON(b, v)
	case json.Marshaler:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return append(b, encoded...), nil
	}
	if fields, ok := plainStructFields(reflect.TypeOf(value)); ok {
		// The payload structs of generated renderers: exported fields with
		// plain json tags, written in declaration order as encoding/json does.
		rv := reflect.ValueOf(value)
		b = append(b, '{')
		for i, f := range fields {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, f.quoted...)
			var err error
			if b, err = appendJSON(b, rv.Field(f.index).Interface()); err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(b, encoded...), nil
}

func appendCompactJSON(b []byte, raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return append(b, "null"...), nil
	}
	// json.Marshal validates and compacts a RawMessage the same way.
	encoded, err := json.Marshal(json.RawMessage(raw))
	if err != nil {
		return nil, err
	}
	return append(b, encoded...), nil
}

// appendJSONFloat formats like encoding/json: ECMAScript number to string,
// exponent form below 1e-6 and from 1e21, "e-07" written as "e-7".
func appendJSONFloat(b []byte, f float64) ([]byte, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, &json.UnsupportedValueError{Str: strconv.FormatFloat(f, 'g', -1, 64)}
	}
	if f == math.Trunc(f) && f > -1<<53 && f < 1<<53 && (f != 0 || !math.Signbit(f)) {
		// An integer, as ids and counts usually are: the same digits.
		return strconv.AppendInt(b, int64(f), 10), nil
	}
	format := byte('f')
	if abs := math.Abs(f); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	b = strconv.AppendFloat(b, f, format, -1, 64)
	if format == 'e' {
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return b, nil
}

// asciiEscapes[c] is how encoding/json writes the ASCII byte c inside a
// string; an empty entry means the byte is written as is. Taken from
// json.Marshal itself, so both encoders always agree.
var asciiEscapes = func() (table [utf8.RuneSelf]string) {
	for c := 0; c < utf8.RuneSelf; c++ {
		encoded, err := json.Marshal(string(rune(c)))
		if err != nil {
			panic(fmt.Sprintf("vela: json.Marshal of byte %d: %v", c, err))
		}
		if inner := string(encoded[1 : len(encoded)-1]); inner != string(rune(c)) {
			table[c] = inner
		}
	}
	return table
}()

// jsonSafe[c] is true for an ASCII byte written as is: one byte load per
// character instead of a string header in the common case (task 060).
var jsonSafe = func() (table [utf8.RuneSelf]bool) {
	for c, escape := range asciiEscapes {
		table[c] = escape == ""
	}
	return table
}()

func appendJSONString(b []byte, s string) []byte {
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if jsonSafe[c] {
				i++
				continue
			}
			if escape := asciiEscapes[c]; escape != "" {
				b = append(b, s[start:i]...)
				b = append(b, escape...)
				start = i + 1
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b = append(b, s[start:i]...)
			b = append(b, `�`...)
			i += size
			start = i
			continue
		}
		if r == ' ' || r == ' ' {
			b = append(b, s[start:i]...)
			b = append(b, `\u202`...)
			b = append(b, "0123456789abcdef"[r&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	b = append(b, s[start:]...)
	return append(b, '"')
}

type plainField struct {
	index  int
	quoted string // "name":
}

var plainStructs sync.Map // reflect.Type → []plainField, or nil when not plain

// plainStructFields reports the fields of a struct type whose encoding is
// unambiguous: every field exported, not embedded, with a json tag that is
// a plain name (no options, not "-"). Other structs go through
// encoding/json.
func plainStructFields(t reflect.Type) ([]plainField, bool) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, false
	}
	if cached, ok := plainStructs.Load(t); ok {
		fields, _ := cached.([]plainField)
		return fields, fields != nil
	}
	var fields []plainField
	plain := true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if !f.IsExported() || f.Anonymous || tag == "" || tag == "-" || strings.ContainsAny(tag, ",\"\\") {
			plain = false
			break
		}
		fields = append(fields, plainField{index: i, quoted: string(appendJSONString(nil, tag)) + ":"})
	}
	if !plain {
		plainStructs.Store(t, nil)
		return nil, false
	}
	if fields == nil {
		fields = []plainField{}
	}
	plainStructs.Store(t, fields)
	return fields, true
}
