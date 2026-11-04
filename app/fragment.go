package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/elauven/vela/app/internal/protocol"
	vela "github.com/elauven/vela/ssr_runtime"
)

// staticRoots reads vela.manifest.json: the segment ids (P-11) of the
// components whose root mode is static (R-9).
func staticRoots(data []byte) (map[string]bool, error) {
	var manifest struct {
		Version    int `json:"version"`
		Components []struct {
			Source string `json:"source"`
			Mode   string `json:"mode"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.Version != 1 {
		return nil, fmt.Errorf("unsupported manifest version %d", manifest.Version)
	}
	static := map[string]bool{}
	for _, c := range manifest.Components {
		if c.Mode == "static" {
			static[strings.TrimSuffix(c.Source, ".vela")] = true
		}
	}
	return static, nil
}

// sent are the segment ids of a navigation response: the levels from start
// and the error page.
func (o *outcome) sent() []string {
	var ids []string
	for _, l := range o.run.levels[o.start:o.rendered] {
		ids = append(ids, l.level.ID)
	}
	if o.segError != nil {
		ids = append(ids, o.errorID)
	}
	return ids
}

// policy chooses between data and a fragment for a navigation (R-9): the
// mode the client asked for, then the route's Render, then for auto a
// static fragment when every component sent is a static root. A fragment
// is static when its components all are; otherwise the server renders an
// interactive VelaApp root for the client to hydrate.
func (h *Handler) policy(o *outcome, requested protocol.Mode) (fragment, static bool) {
	ids := o.sent()
	if len(ids) == 0 {
		return false, false
	}
	static = h.static != nil
	for _, id := range ids {
		if !h.static[id] || h.routes.Components[id] == nil {
			static = false
		}
	}
	switch requested {
	case protocol.ModeData:
		return false, false
	case protocol.ModeFragment:
		return true, static
	}
	switch o.route.Render {
	case RenderData:
		return false, false
	case RenderFragment:
		return true, static
	}
	return static, static
}

// fragment renders the levels from start (R-8). A static fragment nests
// the components' static renderings, each level the default slot of the
// one above, without markers or payload. An interactive one is the
// VelaApp root with start, its own payload included, which the client
// mounts with serverComponent.
func (h *Handler) fragment(ctx context.Context, o *outcome, route protocol.Route, static bool) (string, error) {
	out := &limitedBuffer{max: h.maxBytes}
	prop := routeProp(route)
	if static {
		type level struct {
			id    string
			props map[string]any
		}
		var levels []level
		for _, l := range o.run.levels[o.start:o.rendered] {
			levels = append(levels, level{l.level.ID, map[string]any{"data": l.view, "route": prop}})
		}
		if o.segError != nil {
			levels = append(levels, level{o.errorID, map[string]any{"error": errorProp(o.segError), "route": prop}})
		}
		var render func(i int) func(io.Writer) error
		render = func(i int) func(io.Writer) error {
			return func(w io.Writer) error {
				var slots map[string]func(io.Writer) error
				if i+1 < len(levels) {
					slots = map[string]func(io.Writer) error{"default": render(i + 1)}
				}
				return h.routes.Components[levels[i].id].RenderDynamic(ctx, w, levels[i].props, slots, vela.DynamicOptions{Static: true})
			}
		}
		if err := render(0)(out); err != nil {
			return "", err
		}
	} else {
		chain := make([]any, 0, o.rendered+1)
		for i, l := range o.run.levels[:o.rendered] {
			entry := map[string]any{"id": l.level.ID, "key": o.keys[i]}
			if i >= o.start {
				// The levels above start are the client's; the fragment
				// renders from start.
				entry["data"] = l.view
			}
			chain = append(chain, entry)
		}
		if o.segError != nil {
			chain = append(chain, map[string]any{"id": o.errorID, "key": o.errorKey(), "error": errorProp(o.segError)})
		}
		if err := h.routes.Root.Render(ctx, out, RootProps{Route: prop, Chain: chain, Start: o.start}); err != nil {
			return "", err
		}
	}
	if out.err != nil {
		return "", out.err
	}
	if out.Len() == 0 {
		return "", errors.New("app: the fragment is empty")
	}
	return out.String(), nil
}

// errorProp is the error prop of Error.vela (R-1).
func errorProp(e *protocol.SegmentError) map[string]any {
	return map[string]any{"status": float64(e.Status), "message": e.Message}
}
