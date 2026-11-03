package app

import (
	"fmt"
	"html"
	"net/http"
	"strings"
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

// mergeHead merges the defaults and the heads of the rendered segments in
// level order (R-6): the deepest title wins, and a deeper meta tag
// replaces one with the same name or property.
func mergeHead(defaults Head, segments []Head) Head {
	merged := Head{Title: defaults.Title}
	for _, m := range defaults.Meta {
		merged.SetMeta(m)
	}
	for _, h := range segments {
		if h.Title != "" {
			merged.Title = h.Title
		}
		for _, m := range h.Meta {
			merged.SetMeta(m)
		}
	}
	return merged
}

// html writes the head tags. The tags the app sets carry data-vela-head,
// so the client can replace them on navigation.
func (h Head) html() string {
	var b strings.Builder
	if h.Title != "" {
		fmt.Fprintf(&b, "<title>%s</title>", html.EscapeString(h.Title))
	}
	for _, m := range h.Meta {
		attr, name := "name", m.Name
		if m.Property != "" {
			attr, name = "property", m.Property
		}
		fmt.Fprintf(&b, `<meta %s="%s" content="%s" data-vela-head>`, attr, html.EscapeString(name), html.EscapeString(m.Content))
	}
	return b.String()
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
