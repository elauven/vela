package app

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/elauven/vela/app/internal/protocol"
	vela "github.com/elauven/vela/ssr_runtime"
)

// Head is the title and <meta> tags of a document (docs/APP.md R-6):
// Config.Head holds the defaults, and r.Head() the tags of one segment.
type Head struct {
	Title string
	Meta  []Meta
}

// Meta is one <meta> tag: Name or Property, and Content.
type Meta struct {
	Name     string
	Property string
	Content  string
}

// SetMeta sets the tag with the meta's name or property, replacing an
// earlier one.
func (h *Head) SetMeta(m Meta) {
	for i, old := range h.Meta {
		if old.key() == m.key() {
			h.Meta[i] = m
			return
		}
	}
	h.Meta = append(h.Meta, m)
}

func (m Meta) key() string {
	if m.Property != "" {
		return "property=" + m.Property
	}
	return "name=" + m.Name
}

// levelHead is the head r.Head() of the loader of chain level index.
type levelHead struct {
	level int
	head  Head
}

// mergedHead is the head of the app: Config.Head and the heads of the
// levels, with the depth of the component that each value belongs to. The
// defaults have depth 0, level i depth i+1, the depth of its component
// under VelaApp (task 091): a <vela:head> of a deeper component replaces
// them, and one of the level's own component too.
type mergedHead struct {
	Head
	titleDepth int
	metaDepth  []int
}

// mergeHead merges the defaults and the heads of the rendered levels in
// level order (R-6): the deepest title wins, and a deeper meta tag
// replaces one with the same name or property.
func mergeHead(defaults Head, levels []levelHead) mergedHead {
	merged := mergedHead{Head: Head{Title: defaults.Title}}
	set := func(m Meta, depth int) {
		for i, old := range merged.Meta {
			if old.key() == m.key() {
				merged.Meta[i], merged.metaDepth[i] = m, depth
				return
			}
		}
		merged.Meta = append(merged.Meta, m)
		merged.metaDepth = append(merged.metaDepth, depth)
	}
	for _, m := range defaults.Meta {
		set(m, 0)
	}
	for _, l := range levels {
		if l.head.Title != "" {
			merged.Title, merged.titleDepth = l.head.Title, l.level+1
		}
		for _, m := range l.head.Meta {
			set(m, l.level+1)
		}
	}
	return merged
}

// tags are the app's tags as head tags of components: key, depth and the
// tag with data-vela-head="app:<depth>", by which the client finds and
// replaces them on navigation.
func (h mergedHead) tags() []vela.HeadTag {
	var tags []vela.HeadTag
	if h.Title != "" {
		tags = append(tags, vela.HeadTag{Key: "title", Depth: h.titleDepth, Order: -1,
			HTML: fmt.Sprintf(`<title data-vela-head="app:%d">%s</title>`, h.titleDepth, html.EscapeString(h.Title))})
	}
	for i, m := range h.Meta {
		attr, name, key := "name", m.Name, "meta name="+m.Name
		if m.Property != "" {
			attr, name, key = "property", m.Property, "meta property="+m.Property
		}
		tags = append(tags, vela.HeadTag{Key: key, Depth: h.metaDepth[i], Order: -1,
			HTML: fmt.Sprintf(`<meta %s="%s" content="%s" data-vela-head="app:%d">`, attr, html.EscapeString(name), html.EscapeString(m.Content), h.metaDepth[i])})
	}
	return tags
}

// html writes the head of a document: the app's tags merged with the
// tags of the components' <vela:head> (task 091). A key goes to the
// deepest value, at the same depth to a component's.
func (h mergedHead) html(components []vela.HeadTag) string {
	all := append(h.tags(), components...)
	winner := map[string]int{}
	for i, tag := range all {
		if tag.Key == "" {
			continue
		}
		if best, ok := winner[tag.Key]; !ok || tag.Depth > all[best].Depth || (tag.Depth == all[best].Depth && tag.Order > all[best].Order) {
			winner[tag.Key] = i
		}
	}
	var b strings.Builder
	for i, tag := range all {
		if tag.Key == "" || winner[tag.Key] == i {
			b.WriteString(tag.HTML)
		}
	}
	return b.String()
}

// protocol is the head of a data response, with the depths the client
// registers the tags with (N-5).
func (h mergedHead) protocol() *protocol.Head {
	head := &protocol.Head{Title: h.Title, TitleDepth: h.titleDepth}
	for i, m := range h.Meta {
		head.Meta = append(head.Meta, protocol.Meta{Name: m.Name, Property: m.Property, Content: m.Content, Depth: h.metaDepth[i]})
	}
	return head
}

// The placeholders of a document shell (R-4).
const (
	headPlaceholder = "%vela.head%"
	bodyPlaceholder = "%vela.body%"
)

// defaultShell is the built-in document shell.
const defaultShell = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
%vela.head%
</head>
<body>
%vela.body%
</body>
</html>
`

// shell is a document split at its placeholders.
type shell struct {
	beforeHead, betweenHeadBody, afterBody string
}

// parseShell checks that each placeholder appears exactly once, the head
// before the body (R-4).
func parseShell(source, name string) (shell, error) {
	for _, p := range []string{headPlaceholder, bodyPlaceholder} {
		if n := strings.Count(source, p); n != 1 {
			return shell{}, fmt.Errorf("app: %s must contain %s exactly once, not %d times", name, p, n)
		}
	}
	head := strings.Index(source, headPlaceholder)
	body := strings.Index(source, bodyPlaceholder)
	if body < head {
		return shell{}, fmt.Errorf("app: %s has %s before %s", name, bodyPlaceholder, headPlaceholder)
	}
	return shell{
		beforeHead:      source[:head],
		betweenHeadBody: source[head+len(headPlaceholder) : body],
		afterBody:       source[body+len(bodyPlaceholder):],
	}, nil
}

// builtinErrorPage is the page of an error without an Error.vela (R-3).
func builtinErrorPage(status int, message string) string {
	text := html.EscapeString(fmt.Sprintf("%d %s", status, http.StatusText(status)))
	return "<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<title>" +
		text + "</title>\n</head>\n<body>\n<h1>" + text + "</h1>\n<p>" + html.EscapeString(message) + "</p>\n</body>\n</html>\n"
}
