package app

import (
	"encoding/json"
	"slices"
)

// chainEntry is a level of the route chain in a document's root props: the
// renderers read it as an object with id, key, data and the other fields.
// The JSON of its data, written while the view was built (internal/value),
// rides under entryDataJSON, which the hydration payload embeds instead of
// encoding the view again.
type chainEntry map[string]any

// entryDataJSON is the key of the data's JSON. Translated code reads the
// entry by its field names only (VelaApp.vela); the key is not written to
// any JSON.
const entryDataJSON = "\x00vela:dataJSON"

// AppendPayloadJSON writes the entry as the hydration payload writes an
// object, with the data's JSON as it is (ssr_runtime.PayloadAppender).
func (e chainEntry) AppendPayloadJSON(b []byte, appendValue func([]byte, any) ([]byte, error)) ([]byte, error) {
	keys := make([]string, 0, len(e))
	for k := range e {
		if k != entryDataJSON {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	raw, _ := e[entryDataJSON].(json.RawMessage)
	b = append(b, '{')
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		var err error
		if b, err = appendValue(b, k); err != nil {
			return nil, err
		}
		b = append(b, ':')
		if k == "data" && raw != nil {
			b = append(b, raw...)
			continue
		}
		if b, err = appendValue(b, e[k]); err != nil {
			return nil, err
		}
	}
	return append(b, '}'), nil
}

// MarshalJSON writes the entry without the data's JSON key, for a runtime
// without PayloadAppender and for any other encoder.
func (e chainEntry) MarshalJSON() ([]byte, error) {
	return e.AppendPayloadJSON(nil, func(b []byte, v any) ([]byte, error) {
		encoded, err := json.Marshal(v)
		return append(b, encoded...), err
	})
}
