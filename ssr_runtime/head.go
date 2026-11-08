package ssr_runtime

import (
	"context"
	"io"
	"strings"
	"sync"
)

// Document head (task 091). A component's <vela:head> renders its tags into
// the head collector of the render context instead of the body. Tags with
// the same key ("title", "meta name=…", "meta property=…", "link
// canonical") replace each other: the tag of the deepest component wins, at
// the same depth the later one. A render without a collector renders no
// head tags; the browser creates them when it hydrates.

// HeadTag is one tag of a <vela:head>.
type HeadTag struct {
	// Key names the tags that replace each other; "" for a tag every
	// component may add (a JSON-LD script, a link other than canonical).
	Key string
	// HTML is the tag with its data-vela-head attribute.
	HTML string
	// Depth is the depth of the component: 0 for the root, one more per
	// nested component.
	Depth int
	// Order is the position among the tags of the render.
	Order int
}

// Head collects the head tags of a render.
type Head struct {
	mu   sync.Mutex
	tags []HeadTag
}

type headKey struct{}

// WithHead makes the renders under ctx collect their head tags into head.
func WithHead(ctx context.Context, head *Head) context.Context {
	return context.WithValue(ctx, headKey{}, head)
}

func headOf(w io.Writer) *Head {
	head, _ := ContextOf(w).Value(headKey{}).(*Head)
	return head
}

// Tags returns the winning tags in render order (see Head).
func (h *Head) Tags() []HeadTag {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	winners := map[string]int{}
	for i, tag := range h.tags {
		if tag.Key == "" {
			continue
		}
		if best, ok := winners[tag.Key]; !ok || tag.Depth > h.tags[best].Depth || (tag.Depth == h.tags[best].Depth && tag.Order > h.tags[best].Order) {
			winners[tag.Key] = i
		}
	}
	out := make([]HeadTag, 0, len(h.tags))
	for i, tag := range h.tags {
		if tag.Key == "" || winners[tag.Key] == i {
			out = append(out, tag)
		}
	}
	return out
}

// HTML writes the winning tags.
func (h *Head) HTML() string {
	var b strings.Builder
	for _, tag := range h.Tags() {
		b.WriteString(tag.HTML)
	}
	return b.String()
}

func (h *Head) len() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.tags)
}

// truncate drops the tags after the first n: those of a failed boundary.
func (h *Head) truncate(n int) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if n < len(h.tags) {
		h.tags = h.tags[:n]
	}
}

// WriteHeadTag renders one head tag of the component rendering to w into
// the collector of the render context. Generated code calls it.
func WriteHeadTag(w io.Writer, key string, render func(io.Writer) error) error {
	head := headOf(w)
	if head == nil {
		return nil
	}
	depth := 0
	parent, _ := w.(*streamWriter)
	if parent != nil {
		depth = parent.out.depth - 1
	}
	var b strings.Builder
	tagWriter := &streamWriter{out: newOut(&b), static: true}
	if parent != nil {
		// Hosts called by the tag's expressions get the render context.
		tagWriter.ctx = parent.ctx
	}
	if err := render(tagWriter); err != nil {
		return err
	}
	if err := tagWriter.out.flush(); err != nil {
		return err
	}
	head.mu.Lock()
	head.tags = append(head.tags, HeadTag{Key: key, HTML: b.String(), Depth: max(depth, 0), Order: len(head.tags)})
	head.mu.Unlock()
	return nil
}

// WriteScriptText writes the text of a JSON-LD script: "<" becomes \u003c,
// which JSON reads as "<", so the text cannot close the script element.
func WriteScriptText(w io.Writer, text string) error {
	return WriteString(w, strings.ReplaceAll(text, "<", "\\u003c"))
}
