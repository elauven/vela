// Package protocol defines the navigation protocol between the client
// router (@govela/app/client) and the Go server (docs/APP.md N- rules): the
// request headers, the JSON responses, segment keys and the version check.
//
// The JSON field names and header formats are normative (N-8); the Go
// names are not.
package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Version is the protocol version of this package (N-1).
const Version = 1

// Request headers (N-2).
const (
	HeaderNavigate = "X-Vela-Navigate"
	HeaderHave     = "X-Vela-Have"
	HeaderBuild    = "X-Vela-Build"
	// HeaderPurpose is "prefetch" on a prefetch of the router (G-4): its
	// answer sets no cookies.
	HeaderPurpose = "X-Vela-Purpose"
)

// Vary is the value of the Vary header of every app response (R-7).
const Vary = HeaderNavigate + ", " + HeaderHave + ", " + HeaderBuild + ", " + HeaderPurpose

// IsPrefetch reports a prefetch request of the router.
func IsPrefetch(h http.Header) bool {
	return h.Get(HeaderPurpose) == "prefetch"
}

// CacheOnce is the value of the cache field of a user-dependent response
// (G-4): the router's prefetch cache serves it to one navigation.
const CacheOnce = "once"

// Limits of X-Vela-Have and keys (N-3).
const (
	MaxHaveKeys  = 32
	MaxHaveBytes = 4096
	MaxKeyLen    = 64
)

// Mode is the response mode a navigation request asks for (N-2).
type Mode string

const (
	ModeAuto     Mode = "auto"
	ModeData     Mode = "data"
	ModeFragment Mode = "fragment"
)

// ErrVersion reports a navigation request or response of another protocol
// version. The server answers it with a reload (N-1).
var ErrVersion = errors.New("vela navigation protocol: version mismatch")

// Request is a parsed navigation request.
type Request struct {
	Mode  Mode
	Have  []string // keys the client holds, in level order; nil when absent or malformed
	Build string
}

// ParseRequest reads the navigation headers. ok is false for a document
// request (no X-Vela-Navigate). An unknown mode or another version is
// ErrVersion: the server answers reload. A malformed X-Vela-Have is not an
// error; it is treated as empty (N-3).
func ParseRequest(h http.Header) (req Request, ok bool, err error) {
	nav := h.Values(HeaderNavigate)
	if len(nav) == 0 {
		return Request{}, false, nil
	}
	if len(nav) > 1 {
		return Request{}, true, fmt.Errorf("%w: %d %s headers", ErrVersion, len(nav), HeaderNavigate)
	}
	mode, err := ParseNavigate(nav[0])
	if err != nil {
		return Request{}, true, err
	}
	have := h.Values(HeaderHave)
	req = Request{Mode: mode, Build: h.Get(HeaderBuild)}
	if len(have) == 1 {
		req.Have = ParseHave(have[0])
	}
	return req, true, nil
}

// ParseNavigate parses an X-Vela-Navigate value: "<mode>;v=<version>".
func ParseNavigate(value string) (Mode, error) {
	mode, params, _ := strings.Cut(value, ";")
	mode = strings.TrimSpace(mode)
	version := ""
	for p := range strings.SplitSeq(params, ";") {
		name, val, _ := strings.Cut(strings.TrimSpace(p), "=")
		if name == "v" {
			version = val
		}
	}
	if version != strconv.Itoa(Version) {
		return "", fmt.Errorf("%w: %s %q", ErrVersion, HeaderNavigate, value)
	}
	switch m := Mode(mode); m {
	case ModeAuto, ModeData, ModeFragment:
		return m, nil
	}
	return "", fmt.Errorf("%w: unknown mode in %s %q", ErrVersion, HeaderNavigate, value)
}

// FormatNavigate is the X-Vela-Navigate value of a request for mode.
func FormatNavigate(mode Mode) string {
	return string(mode) + ";v=" + strconv.Itoa(Version)
}

// ParseHave parses X-Vela-Have: keys separated by ",". It returns nil for an
// empty or malformed value, or one over the limits (N-3), so the server
// renders from level 0.
func ParseHave(value string) []string {
	if value == "" || len(value) > MaxHaveBytes {
		return nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > MaxHaveKeys {
		return nil
	}
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if !ValidKey(p) {
			return nil
		}
		parts[i] = p
	}
	return parts
}

// FormatHave is the X-Vela-Have value for keys.
func FormatHave(keys []string) string {
	return strings.Join(keys, ",")
}

// ValidKey reports whether k is a key of N-3: 1 to MaxKeyLen characters of
// [A-Za-z0-9_-].
func ValidKey(k string) bool {
	if k == "" || len(k) > MaxKeyLen {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// SegmentKey computes the key of a segment from its id and the values of
// its dependencies (L-6), in a fixed order chosen by the caller, names
// included (for example "id", "42", "?q", "shoes"). Every part is length
// prefixed, so different parts never produce the same input. The key is 22
// characters of unpadded base64url (128 bits of SHA-256).
func SegmentKey(id string, deps ...string) string {
	h := sha256.New()
	var n [binary.MaxVarintLen64]byte
	for _, part := range append([]string{id}, deps...) {
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(part)))])
		h.Write([]byte(part))
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:16])
}

// Start is the first level whose key differs from the key the client holds
// at that level, or the end of the shorter chain when they all match (N-4).
func Start(have, keys []string) int {
	i := 0
	for i < len(have) && i < len(keys) && have[i] == keys[i] {
		i++
	}
	return i
}

// Type is the type of a response (N-5).
type Type string

const (
	TypeData     Type = "data"
	TypeFragment Type = "fragment"
	TypeRedirect Type = "redirect"
	TypeReload   Type = "reload"
	// TypeForm is the failure of a form action (app.Fail, task 078): the
	// page level gets Form as its form prop.
	TypeForm Type = "form"
)

// Reasons of a reload response (N-5).
const (
	ReasonBuild    = "build"
	ReasonProtocol = "protocol"
	ReasonDocument = "document"
)

// Route is the route prop of the current navigation (R-1).
type Route struct {
	ID     string            `json:"id"`
	URL    string            `json:"url"`
	Path   string            `json:"path"`
	Params map[string]string `json:"params"`
	Query  map[string]string `json:"query"`
}

// Segment is one chain level (N-5): either Data or Error.
type Segment struct {
	ID  string
	Key string
	// Depends are the dependency keys of the level (Deps.Keys, task 097),
	// which invalidate(key) in the client matches. Optional: an older
	// client ignores it.
	Depends []string
	Data    json.RawMessage // the loader result; nil is null
	Error   *SegmentError
}

// SegmentError is the error of the Error.vela level that ends a chain (R-3).
type SegmentError struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

type wireSegment struct {
	ID      string          `json:"id"`
	Key     string          `json:"key"`
	Depends []string        `json:"depends,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   *SegmentError   `json:"error,omitempty"`
}

// MarshalJSON writes data, null data, or error, never both.
func (s Segment) MarshalJSON() ([]byte, error) {
	w := wireSegment{ID: s.ID, Key: s.Key, Depends: s.Depends, Error: s.Error}
	if s.Error == nil {
		w.Data = s.Data
		if w.Data == nil {
			w.Data = json.RawMessage("null")
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads a segment; null data stays nil.
func (s *Segment) UnmarshalJSON(b []byte) error {
	var w wireSegment
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*s = Segment{ID: w.ID, Key: w.Key, Depends: w.Depends, Error: w.Error}
	if string(w.Data) != "null" {
		s.Data = w.Data
	}
	return nil
}

// Head is the merged head of a response (R-6).
type Head struct {
	Title string `json:"title,omitempty"`
	// TitleDepth is the depth of the component the title belongs to: 0
	// for Config.Head, i+1 for chain level i (task 091). The client
	// registers the tag with it, so that a deeper <vela:head> wins.
	TitleDepth int    `json:"titleDepth,omitempty"`
	Meta       []Meta `json:"meta,omitempty"`
}

// Meta is one <meta> tag set by a loader.
type Meta struct {
	Name     string `json:"name,omitempty"`
	Property string `json:"property,omitempty"`
	Content  string `json:"content"`
	// Depth is as Head.TitleDepth.
	Depth int `json:"depth,omitempty"`
}

// Fragment is the HTML of a fragment response (R-8).
type Fragment struct {
	HTML   string `json:"html"`
	Static bool   `json:"static"`
}

// Response is a navigation response. Which fields are used depends on Type
// (N-5); Encode checks them.
type Response struct {
	Type  Type
	Build string

	// data and fragment
	Route    *Route
	Start    int
	Segments []Segment
	Head     *Head     // nil: the client keeps its head
	Fragment *Fragment // fragment only

	// redirect, and the status of a form failure
	Location string
	Status   int

	// form
	Form json.RawMessage

	// reload
	Reason string

	// Cache is CacheOnce for a user-dependent data or fragment response,
	// "" otherwise. Optional (N-5): clients ignore fields they do not know,
	// and an optional field does not change the version.
	Cache string
}

type wireResponse struct {
	V        int             `json:"v"`
	Type     Type            `json:"type"`
	Build    string          `json:"build"`
	Route    *Route          `json:"route,omitempty"`
	Start    *int            `json:"start,omitempty"`
	Segments *[]Segment      `json:"segments,omitempty"`
	Head     *Head           `json:"head,omitempty"`
	Fragment *Fragment       `json:"fragment,omitempty"`
	Location string          `json:"location,omitempty"`
	Status   int             `json:"status,omitempty"`
	Form     json.RawMessage `json:"form,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Cache    string          `json:"cache,omitempty"`
}

// Encode validates r and returns its JSON.
func Encode(r Response) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	w := wireResponse{V: Version, Type: r.Type, Build: r.Build}
	switch r.Type {
	case TypeData, TypeFragment:
		route := *r.Route
		if route.Params == nil {
			route.Params = map[string]string{}
		}
		if route.Query == nil {
			route.Query = map[string]string{}
		}
		segments := r.Segments
		if segments == nil {
			segments = []Segment{}
		}
		w.Route, w.Start, w.Segments, w.Head, w.Cache = &route, &r.Start, &segments, r.Head, r.Cache
		if r.Type == TypeFragment {
			w.Fragment = r.Fragment
		}
	case TypeRedirect:
		w.Location, w.Status = r.Location, r.Status
	case TypeReload:
		w.Reason = r.Reason
	case TypeForm:
		w.Status, w.Form = r.Status, r.Form
		if w.Form == nil {
			w.Form = json.RawMessage("null")
		}
	}
	return json.Marshal(w)
}

// Decode parses a response. Another version is ErrVersion, an unknown type
// or an invalid response is an error; a client handles both as reload
// (N-5).
func Decode(b []byte) (Response, error) {
	var w wireResponse
	if err := json.Unmarshal(b, &w); err != nil {
		return Response{}, fmt.Errorf("vela navigation protocol: %w", err)
	}
	if w.V != Version {
		return Response{}, fmt.Errorf("%w: response version %d", ErrVersion, w.V)
	}
	r := Response{
		Type: w.Type, Build: w.Build, Route: w.Route, Head: w.Head, Fragment: w.Fragment,
		Location: w.Location, Status: w.Status, Reason: w.Reason, Form: w.Form, Cache: w.Cache,
	}
	if w.Start != nil {
		r.Start = *w.Start
	} else if w.Type == TypeData || w.Type == TypeFragment {
		r.Start = -1 // reported by Validate
	}
	if w.Segments != nil {
		r.Segments = *w.Segments
	}
	if err := r.Validate(); err != nil {
		return Response{}, err
	}
	return r, nil
}

// Validate checks the fields that Type requires (N-5).
func (r Response) Validate() error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("vela navigation protocol: %s response: %s", r.Type, fmt.Sprintf(format, args...))
	}
	if r.Build == "" {
		return fail("no build id")
	}
	switch r.Type {
	case TypeData, TypeFragment:
		if r.Route == nil {
			return fail("no route")
		}
		if r.Start < 0 {
			return fail("no start")
		}
		for i, s := range r.Segments {
			if s.ID == "" {
				return fail("segment %d has no id", i)
			}
			if !ValidKey(s.Key) {
				return fail("segment %s has an invalid key %q", s.ID, s.Key)
			}
			if s.Error != nil && i != len(r.Segments)-1 {
				return fail("error segment %s is not the last one", s.ID)
			}
		}
		if r.Type == TypeFragment && r.Fragment == nil {
			return fail("no fragment")
		}
		if r.Type == TypeData && r.Fragment != nil {
			return fail("a fragment in a data response")
		}
	case TypeRedirect:
		if r.Location == "" {
			return fail("no location")
		}
		switch r.Status {
		case 301, 302, 303, 307, 308:
		default:
			return fail("status %d is not a redirect", r.Status)
		}
	case TypeForm:
		if r.Status < 400 || r.Status > 599 {
			return fail("status %d is not an error", r.Status)
		}
	case TypeReload:
		switch r.Reason {
		case ReasonBuild, ReasonProtocol, ReasonDocument:
		default:
			return fail("unknown reason %q", r.Reason)
		}
	default:
		return fmt.Errorf("vela navigation protocol: unknown response type %q", r.Type)
	}
	return nil
}
