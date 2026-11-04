package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"html"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/elauven/vela/app/internal/protocol"
	vela "github.com/elauven/vela/ssr_runtime"
)

// fakeComponent renders <div data-id="…">data, route path, slot</div> and
// records the options it got.
type fakeComponent struct {
	id     string
	static *[]bool
}

func (c fakeComponent) ComponentID() string { return c.id }

func (c fakeComponent) RenderDynamic(ctx context.Context, w io.Writer, props map[string]any, slots map[string]func(io.Writer) error, options vela.DynamicOptions) error {
	*c.static = append(*c.static, options.Static)
	data, _ := json.Marshal(props["data"])
	if e, ok := props["error"]; ok {
		data, _ = json.Marshal(e)
	}
	path := props["route"].(map[string]any)["path"]
	fmt.Fprintf(w, `<div data-id="%s">%s %v`, c.id, data, path)
	if slot := slots["default"]; slot != nil {
		if err := slot(w); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "</div>")
	return err
}

// fragmentHandler serves navRoutes with a manifest in which the given ids
// are static roots, and fake components for every level.
func fragmentHandler(t *testing.T, static ...string) (*Handler, *[]bool) {
	t.Helper()
	routes, _ := navRoutes()
	var options []bool
	routes.Components = map[string]vela.Component{}
	for _, id := range []string{"routes/Layout", "routes/s/Layout", "routes/s/[id]/Page", "routes/s/Error", "routes/Error", "routes/doc/Page", "routes/bare/[id]/Page"} {
		routes.Components[id] = fakeComponent{id: id, static: &options}
	}
	type entry struct {
		Source string `json:"source"`
		Mode   string `json:"mode"`
	}
	manifest := struct {
		Version    int     `json:"version"`
		Components []entry `json:"components"`
	}{Version: 1}
	for id := range routes.Components {
		mode := "interactive"
		for _, s := range static {
			if s == id {
				mode = "static"
			}
		}
		manifest.Components = append(manifest.Components, entry{Source: id + ".vela", Mode: mode})
	}
	data, _ := json.Marshal(manifest)
	file := filepath.Join(t.TempDir(), "vela.manifest.json")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return newHandler(t, Config{Routes: routes, Manifest: file}), &options
}

func navigateMode(mode protocol.Mode, have []string) map[string]string {
	return map[string]string{protocol.HeaderNavigate: protocol.FormatNavigate(mode), protocol.HeaderHave: protocol.FormatHave(have)}
}

// TestFragmentPolicy: R-9 for auto, explicit modes, and the route's Render.
func TestFragmentPolicy(t *testing.T) {
	sectionStatic := []string{"routes/Layout", "routes/s/Layout", "routes/s/[id]/Page", "routes/s/Error", "routes/Error"}
	cases := []struct {
		name    string
		static  []string
		target  string
		mode    protocol.Mode
		keep    bool // send the keys of /s/1?q=a
		want    protocol.Type
		statics bool
	}{
		{name: "auto, all static", static: sectionStatic, target: "/s/2?q=a", mode: protocol.ModeAuto, keep: true, want: protocol.TypeFragment, statics: true},
		{name: "auto, a level is interactive", static: []string{"routes/Layout", "routes/s/Layout"}, target: "/s/2?q=a", mode: protocol.ModeAuto, keep: true, want: protocol.TypeData},
		{name: "auto, only the kept levels are static", static: []string{"routes/s/[id]/Page"}, target: "/s/2?q=a", mode: protocol.ModeAuto, keep: true, want: protocol.TypeFragment, statics: true},
		{name: "auto, a sent level is interactive", static: []string{"routes/s/[id]/Page"}, target: "/s/2?q=b", mode: protocol.ModeAuto, keep: true, want: protocol.TypeData},
		{name: "auto, error page static", static: []string{"routes/s/Error"}, target: "/s/9?q=a", mode: protocol.ModeAuto, keep: true, want: protocol.TypeFragment, statics: true},
		{name: "explicit data", static: sectionStatic, target: "/s/2?q=a", mode: protocol.ModeData, keep: true, want: protocol.TypeData},
		{name: "explicit fragment, interactive", target: "/s/2?q=a", mode: protocol.ModeFragment, keep: true, want: protocol.TypeFragment},
		{name: "explicit fragment, static", static: sectionStatic, target: "/s/2?q=a", mode: protocol.ModeFragment, keep: true, want: protocol.TypeFragment, statics: true},
		{name: "nothing sent", static: sectionStatic, target: "/s/1?q=a&x=1", mode: protocol.ModeAuto, keep: true, want: protocol.TypeData},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _ := fragmentHandler(t, c.static...)
			var have []string
			if c.keep {
				have = keysOf(t, h, "/s/1?q=a")
			}
			resp, _ := navigate(t, h, c.target, navigateMode(c.mode, have))
			if resp.Type != c.want {
				t.Fatalf("type %s, want %s", resp.Type, c.want)
			}
			if c.want == protocol.TypeFragment && resp.Fragment.Static != c.statics {
				t.Errorf("static %v, want %v", resp.Fragment.Static, c.statics)
			}
		})
	}
	// Without a manifest auto is data.
	routes, _ := navRoutes()
	h := newHandler(t, Config{Routes: routes})
	if resp, _ := navigate(t, h, "/s/2", navigateMode(protocol.ModeAuto, nil)); resp.Type != protocol.TypeData {
		t.Errorf("no manifest: %s", resp.Type)
	}
}

func TestStaticFragment(t *testing.T) {
	h, options := fragmentHandler(t, "routes/Layout", "routes/s/Layout", "routes/s/[id]/Page", "routes/s/Error")
	from := keysOf(t, h, "/s/1?q=a")
	*options = nil
	resp, _ := navigate(t, h, "/s/2?q=b", navigateMode(protocol.ModeAuto, from))
	want := `<div data-id="routes/s/Layout">{"q":"b"} /s/2<div data-id="routes/s/[id]/Page">{"id":"2"} /s/2</div></div>`
	if resp.Type != protocol.TypeFragment || !resp.Fragment.Static || resp.Fragment.HTML != want {
		t.Fatalf("%s static=%v\n%q\nwant %q", resp.Type, resp.Fragment != nil && resp.Fragment.Static, resp.Fragment.HTML, want)
	}
	for _, s := range *options {
		if !s {
			t.Error("a level rendered without Static")
		}
	}
	if resp.Start != 1 || len(resp.Segments) != 2 {
		t.Fatalf("start %d, %d segments", resp.Start, len(resp.Segments))
	}
	for _, s := range resp.Segments {
		if s.Data != nil || s.Key == "" {
			t.Errorf("segment %s: data %s, key %q; a static level holds no data", s.ID, s.Data, s.Key)
		}
	}
	if resp.Head == nil || resp.Head.Title != "Item 2" {
		t.Errorf("head %+v", resp.Head)
	}
	// The error page is the innermost level of the fragment.
	resp, _ = navigate(t, h, "/s/9?q=a", navigateMode(protocol.ModeAuto, from))
	if want := `<div data-id="routes/s/Error">{"message":"Not Found","status":404} /s/9</div>`; resp.Fragment == nil || resp.Fragment.HTML != want {
		t.Errorf("error fragment %+v, want %q", resp.Fragment, want)
	}
	if last := resp.Segments[len(resp.Segments)-1]; last.Error == nil || last.Error.Status != 404 {
		t.Errorf("error segment %+v", last)
	}
}

func TestInteractiveFragment(t *testing.T) {
	h, _ := fragmentHandler(t)
	from := keysOf(t, h, "/s/1?q=a")
	resp, _ := navigate(t, h, "/s/2?q=a", navigateMode(protocol.ModeFragment, from))
	if resp.Type != protocol.TypeFragment || resp.Fragment.Static {
		t.Fatalf("%s %+v", resp.Type, resp.Fragment)
	}
	// The fake root writes its props: the VelaApp root with start, whose
	// levels above start carry no data.
	m := propsAttr.FindStringSubmatch(resp.Fragment.HTML)
	if m == nil {
		t.Fatalf("no root: %s", resp.Fragment.HTML)
	}
	var props struct {
		Route map[string]any   `json:"route"`
		Chain []map[string]any `json:"chain"`
		Start int              `json:"start"`
	}
	if err := json.Unmarshal([]byte(unescape(m[1])), &props); err != nil {
		t.Fatal(err)
	}
	if props.Start != 2 || len(props.Chain) != 3 || props.Route["path"] != "/s/2" {
		t.Fatalf("props %+v", props)
	}
	for i, entry := range props.Chain {
		_, hasData := entry["data"]
		if hasData != (i >= 2) || entry["key"] != from[i] && i < 2 {
			t.Errorf("chain[%d] = %v", i, entry)
		}
	}
	if string(resp.Segments[0].Data) != `{"id":"2"}` {
		t.Errorf("an interactive fragment sends the data too: %s", resp.Segments[0].Data)
	}
	// A route that says RenderFragment gets one under auto.
	routes, _ := navRoutes()
	routes.Routes[0].Render = RenderFragment
	h2 := newHandler(t, Config{Routes: routes})
	if resp, _ := navigate(t, h2, "/s/2", navigateMode(protocol.ModeAuto, nil)); resp.Type != protocol.TypeFragment || resp.Fragment.Static {
		t.Errorf("RenderFragment: %s", resp.Type)
	}
	// A render error answers reload.
	routes.Root.Render = func(ctx context.Context, w io.Writer, props RootProps) error { return fmt.Errorf("boom") }
	h3 := newHandler(t, Config{Routes: routes, Logger: discardLogger()})
	if resp, _ := navigate(t, h3, "/s/2", navigateMode(protocol.ModeAuto, nil)); resp.Type != protocol.TypeReload || resp.Reason != protocol.ReasonDocument {
		t.Errorf("render error: %s %s", resp.Type, resp.Reason)
	}
}

func TestManifestChecked(t *testing.T) {
	routes, _ := navRoutes()
	dir := t.TempDir()
	for name, data := range map[string]string{"bad.json": "{", "v2.json": `{"version": 2, "components": []}`} {
		file := filepath.Join(dir, name)
		os.WriteFile(file, []byte(data), 0o644)
		if _, err := New(Config{Routes: routes, Manifest: file}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func unescape(s string) string { return html.UnescapeString(s) }
