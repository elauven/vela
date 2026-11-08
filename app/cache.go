package app

import (
	"container/list"
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/elauven/vela/app/internal/protocol"
)

// The response cache of pages that declare `var Cache = app.Cache{…}`
// (task 092): ISR and stale-while-revalidate inside the server, with the
// same rules in Cache-Control for a CDN in front of it.

// CachedResponse is one stored response.
type CachedResponse struct {
	// Path is the URL path of the request, which Purge patterns match.
	Path   string
	Status int
	Header http.Header
	Body   []byte
	// Stored is when the response was rendered.
	Stored time.Time
}

// CacheStore keeps cached responses by key. The default is an in-memory
// store (NewMemoryCache); a shared store (Redis and the like) implements
// the same methods. Every method may be called concurrently.
type CacheStore interface {
	// Get returns the response stored under key; ok is false when there is
	// none.
	Get(ctx context.Context, key string) (resp CachedResponse, ok bool, err error)
	// Set stores a response under key; the store may drop it after ttl.
	Set(ctx context.Context, key string, resp CachedResponse, ttl time.Duration) error
	// Delete drops the response stored under key.
	Delete(ctx context.Context, key string) error
	// Purge drops every response whose Path matches pattern, a path.Match
	// pattern such as "/blog/*"; "*" matches every path, and a pattern
	// ending in "/**" every path below its prefix.
	Purge(ctx context.Context, pattern string) error
}

// PathMatches reports whether a response path matches a Purge pattern:
// path.Match, plus "*" for every path and "/prefix/**" for every path
// below /prefix. Stores implement Purge with it.
func PathMatches(pattern, p string) bool {
	if pattern == "*" || pattern == "/**" {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
		return p == prefix || strings.HasPrefix(p, prefix+"/")
	}
	ok, err := path.Match(pattern, p)
	return err == nil && ok
}

// memoryCache is the default store: a least recently used set of
// responses whose bodies together stay under a size limit.
type memoryCache struct {
	mu      sync.Mutex
	max     int
	size    int
	order   *list.List // front: most recently used
	entries map[string]*list.Element
	now     func() time.Time
}

type memoryEntry struct {
	key     string
	resp    CachedResponse
	expires time.Time
}

// NewMemoryCache returns an in-memory CacheStore that keeps responses of
// up to maxBytes in total, dropping the least recently used first; 64 MiB
// when maxBytes is 0.
func NewMemoryCache(maxBytes int) CacheStore {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	return &memoryCache{max: maxBytes, order: list.New(), entries: map[string]*list.Element{}, now: time.Now}
}

func entrySize(e *memoryEntry) int {
	n := len(e.key) + len(e.resp.Path) + len(e.resp.Body)
	for k, vs := range e.resp.Header {
		n += len(k)
		for _, v := range vs {
			n += len(v)
		}
	}
	return n
}

func (c *memoryCache) Get(_ context.Context, key string) (CachedResponse, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return CachedResponse{}, false, nil
	}
	e := el.Value.(*memoryEntry)
	if !e.expires.IsZero() && !c.now().Before(e.expires) {
		c.remove(el)
		return CachedResponse{}, false, nil
	}
	c.order.MoveToFront(el)
	return e.resp, true, nil
}

func (c *memoryCache) Set(_ context.Context, key string, resp CachedResponse, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.remove(el)
	}
	e := &memoryEntry{key: key, resp: resp}
	if ttl > 0 {
		e.expires = c.now().Add(ttl)
	}
	size := entrySize(e)
	if size > c.max {
		return nil
	}
	c.entries[key] = c.order.PushFront(e)
	c.size += size
	for c.size > c.max {
		c.remove(c.order.Back())
	}
	return nil
}

func (c *memoryCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.remove(el)
	}
	return nil
}

func (c *memoryCache) Purge(_ context.Context, pattern string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, el := range c.entries {
		if PathMatches(pattern, el.Value.(*memoryEntry).resp.Path) {
			c.remove(el)
		}
	}
	return nil
}

func (c *memoryCache) remove(el *list.Element) {
	e := el.Value.(*memoryEntry)
	c.order.Remove(el)
	delete(c.entries, e.key)
	c.size -= entrySize(e)
}

// Purge drops the cached responses whose path matches pattern (see
// CacheStore.Purge), for example from the webhook of a CMS. The next
// request renders them again.
func (h *Handler) Purge(ctx context.Context, pattern string) error {
	return h.cache.Purge(ctx, pattern)
}

// cacheKey is the build, the mode of the request (document or navigation,
// with the levels the client has) and the URL.
func (h *Handler) cacheKey(r *http.Request) string {
	mode := "document"
	if nav := r.Header.Get(protocol.HeaderNavigate); nav != "" {
		mode = nav + "|" + r.Header.Get(protocol.HeaderHave)
	}
	return h.build + "|" + mode + "|" + r.URL.RequestURI()
}

// cacheControl is the Cache-Control of a cached page: the same rules for a
// CDN in front of the server.
func cacheControl(c *Cache) string {
	cc := fmt.Sprintf("public, max-age=%d", int(c.MaxAge/time.Second))
	if c.StaleWhileRevalidate > 0 {
		cc += fmt.Sprintf(", stale-while-revalidate=%d", int(c.StaleWhileRevalidate/time.Second))
	}
	return cc
}

// serveCached answers a GET or HEAD of a page with Cache from the store:
// fresh at once; stale within StaleWhileRevalidate at once, with one
// background render per key; otherwise rendered now and stored when it
// is cacheable: status 200, no cookie set, and neither a cookie nor an
// app.Local read (G-4). X-Vela-Cache says which (hit, stale, miss).
func (h *Handler) serveCached(w http.ResponseWriter, r *http.Request, route *Route, render func(http.ResponseWriter, *http.Request)) {
	key := h.cacheKey(r)
	ctx := r.Context()
	if stored, ok, err := h.cache.Get(ctx, key); err != nil {
		h.logger.Warn("vela app: cache get", "path", r.URL.Path, "err", err)
	} else if ok {
		age := h.now().Sub(stored.Stored)
		switch {
		case age < route.Cache.MaxAge:
			writeCached(w, r, stored, "hit", route.Cache)
			return
		case age < route.Cache.MaxAge+route.Cache.StaleWhileRevalidate:
			writeCached(w, r, stored, "stale", route.Cache)
			h.revalidate(key, r, route, render)
			return
		}
	}
	if r.Method == http.MethodHead {
		render(w, r)
		return
	}
	resp, cacheable := h.renderToCache(r, route, render)
	if cacheable {
		h.store(ctx, key, resp, route.Cache)
	}
	writeCached(w, r, resp, "miss", route.Cache)
}

// renderToCache renders r into a response and reports whether the cache
// may keep it.
func (h *Handler) renderToCache(r *http.Request, route *Route, render func(http.ResponseWriter, *http.Request)) (CachedResponse, bool) {
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	render(rec, r)
	st := stateOf(r.Context())
	user := false
	if st != nil {
		st.mu.Lock()
		user = st.user
		st.mu.Unlock()
	}
	resp := CachedResponse{Path: r.URL.Path, Status: rec.status, Header: rec.header, Body: rec.body, Stored: h.now()}
	cacheable := !user && rec.status == http.StatusOK && len(rec.header.Values("Set-Cookie")) == 0
	if cacheable {
		resp.Header.Set("Cache-Control", cacheControl(route.Cache))
	}
	return resp, cacheable
}

func (h *Handler) store(ctx context.Context, key string, resp CachedResponse, c *Cache) {
	if err := h.cache.Set(ctx, key, resp, c.MaxAge+c.StaleWhileRevalidate); err != nil {
		h.logger.Warn("vela app: cache set", "path", resp.Path, "err", err)
	}
}

// revalidate renders a stale key again in the background, once per key at
// a time within the process.
func (h *Handler) revalidate(key string, r *http.Request, route *Route, render func(http.ResponseWriter, *http.Request)) {
	h.inflightMu.Lock()
	if h.inflight[key] {
		h.inflightMu.Unlock()
		return
	}
	h.inflight[key] = true
	h.inflightMu.Unlock()
	// The request outlives the response: a copy with its own context and
	// state, without cookies, whose reading would make it uncacheable.
	background := r.Clone(context.WithoutCancel(r.Context()))
	background.Header.Del("Cookie")
	background, _ = h.withState(background)
	go func() {
		defer func() {
			h.inflightMu.Lock()
			delete(h.inflight, key)
			h.inflightMu.Unlock()
		}()
		resp, cacheable := h.renderToCache(background, route, render)
		if cacheable {
			h.store(background.Context(), key, resp, route.Cache)
		}
	}()
}

func writeCached(w http.ResponseWriter, r *http.Request, resp CachedResponse, state string, c *Cache) {
	for k, v := range resp.Header {
		w.Header()[k] = append([]string(nil), v...)
	}
	w.Header().Set("X-Vela-Cache", state)
	if state != "miss" || w.Header().Get("Cache-Control") == "" {
		if w.Header().Get("Set-Cookie") == "" && !strings.Contains(w.Header().Get("Cache-Control"), "private") {
			w.Header().Set("Cache-Control", cacheControl(c))
		}
	}
	w.WriteHeader(resp.Status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(resp.Body)
	}
}

// recorder keeps a response in memory.
type recorder struct {
	header http.Header
	status int
	body   []byte
	wrote  bool
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	r.wrote = true
	r.body = append(r.body, p...)
	return len(p), nil
}
