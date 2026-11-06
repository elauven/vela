package typegen

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type Base struct {
	ID      int    `json:"id"`
	Created string `json:"created"`
}

type Status string

type Celsius float64

func (c Celsius) MarshalJSON() ([]byte, error) { return json.Marshal(float64(c)) }

type Node struct {
	Name     string  `json:"name"`
	Children []*Node `json:"children"`
}

type Post struct {
	Base
	Title    string            `json:"title"`
	Draft    bool              `json:"draft,omitempty"`
	Views    int64             `json:"views,string"`
	Body     []byte            `json:"body"`
	Tags     []string          `json:"tags"`
	Meta     map[string]any    `json:"meta"`
	ByYear   map[int][]float64 `json:"byYear"`
	Author   *Node             `json:"author"`
	At       time.Time         `json:"at"`
	Status   Status            `json:"status"`
	Temp     Celsius           `json:"temp"`
	Secret   string            `json:"-"`
	internal int
	Plain    string
	Inline   struct {
		X float64 `json:"x"`
	} `json:"inline"`
}

func TestConvert(t *testing.T) {
	load := func(ctx context.Context, r any) (*Post, error) { return nil, nil }
	var b strings.Builder
	if err := Write(&b, map[string]any{"page": load}, map[string]reflect.Type{"form": reflect.TypeFor[map[string]string]()}, "$data."); err != nil {
		t.Fatal(err)
	}
	var res Result
	if err := json.Unmarshal([]byte(b.String()), &res); err != nil {
		t.Fatal(err)
	}
	if res.Types["page"] != "$data.Post | null" || res.Types["form"] != "Record<string, string>" {
		t.Errorf("types: %v", res.Types)
	}
	for _, want := range []string{
		"export interface Post {\n  id: number;\n  created: string;\n  title: string;\n  draft?: boolean;\n  views: string;\n  body: string;\n  tags: string[];\n  meta: Record<string, unknown>;\n  byYear: Record<string, number[]>;\n  author: Node | null;\n  at: string;\n  status: string;\n  temp: unknown;\n  Plain: string;\n  inline: {\n    x: number;\n  };\n}",
		"export interface Node {\n  name: string;\n  children: (Node | null)[];\n}",
	} {
		if !strings.Contains(res.Declarations, want) {
			t.Errorf("declarations miss\n%s\nin\n%s", want, res.Declarations)
		}
	}
	if strings.Contains(res.Declarations, "Secret") || strings.Contains(res.Declarations, "internal") || strings.Contains(res.Declarations, "interface Base") {
		t.Errorf("absent fields or a flattened embedding are declared:\n%s", res.Declarations)
	}
}

type A struct {
	Name string
}
type B struct {
	Name string
}
type Both struct {
	A
	B
	Own string `json:"own"`
}

func TestDominance(t *testing.T) {
	res := Convert(map[string]reflect.Type{"x": reflect.TypeFor[Both]()}, "")
	if !strings.Contains(res.Declarations, "export interface Both {\n  own: string;\n}") {
		t.Errorf("an ambiguous embedded field is dropped, as encoding/json does:\n%s", res.Declarations)
	}
}
