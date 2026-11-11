package app

import (
	"encoding/json"
	"testing"

	"github.com/elauven/vela/app/internal/value"
	vela "github.com/elauven/vela/ssr_runtime"
)

// A chain entry writes the payload a plain map with the same view writes,
// with the data's JSON embedded instead of encoded again.
func TestChainEntryPayload(t *testing.T) {
	type item struct {
		ID    int      `json:"id"`
		Title string   `json:"title"`
		Tags  []string `json:"tags,omitempty"`
	}
	result := struct {
		Items []item `json:"items"`
		Note  string `json:"note"`
	}{Items: []item{{1, "a </script>", []string{"x"}}, {2, "b & c", nil}}, Note: " "}
	view, data, err := value.FromGo(result)
	if err != nil {
		t.Fatal(err)
	}
	plain := map[string]any{"id": "routes/Page", "key": "k", "data": view, "depends": []string{"cart"}}
	entry := chainEntry{"id": "routes/Page", "key": "k", "data": view, "depends": []string{"cart"}, entryDataJSON: data}

	want, err := vela.MarshalHydrationPayload([]any{plain})
	if err != nil {
		t.Fatal(err)
	}
	got, err := vela.MarshalHydrationPayload([]any{entry})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("payload\n got %s\nwant %s", got, want)
	}

	// Without the runtime's PayloadAppender (encoding/json): the same JSON
	// as the plain map, without the key of the data's JSON.
	marshaled, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	plainJSON, _ := json.Marshal(plain)
	if string(marshaled) != string(plainJSON) {
		t.Errorf("MarshalJSON\n got %s\nwant %s", marshaled, plainJSON)
	}
}
