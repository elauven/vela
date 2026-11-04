// Package value converts loader results to the values the generated
// renderers receive as props (docs/APP.md L-2). A result goes through
// encoding/json and back into any, so the server renders exactly what the
// client receives in JSON: maps, slices, float64 numbers, strings, booleans
// and nil (docs/SSR_TRANSLATION_CONTRACT.md T-5.1). time.Time becomes its
// RFC 3339 string.
package value

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// FromGo returns the JSON view of v and its encoding. A value that does not
// marshal (a channel, a function, NaN) is an error.
func FromGo(v any) (any, json.RawMessage, error) {
	data, err := Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, nil, fmt.Errorf("value: %w", err)
	}
	return out, data, nil
}

// Marshal encodes v as the client receives it. HTML characters are not
// escaped: the document writer escapes what it embeds.
func Marshal(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, fmt.Errorf("value: the result is not JSON: %w", err)
	}
	return json.RawMessage(bytes.TrimSuffix(b.Bytes(), []byte("\n"))), nil
}

// Into converts a JSON view (or any Go value) to T through JSON, as
// app.Parent does with the result of a parent loader.
func Into[T any](v any) (T, error) {
	var out T
	data, err := Marshal(v)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("value: %w", err)
	}
	return out, nil
}
