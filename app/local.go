package app

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/elauven/vela/app/internal/protocol"
)

// Request locals, cookies and user-dependent responses (docs/APP.md G-3,
// G-4).

// Local is a typed value of a request's context, such as the user that a
// net/http middleware found in the session (G-3):
//
//	var User = app.NewLocal[*User]("user")
//
//	r = User.With(r, u)          // in the middleware
//	u, ok := User.Get(ctx)       // in a guard, loader or action
//
// Its key is a value of an unexported type, so no other package can read
// or replace it. Get marks the response as user-dependent (G-4).
type Local[T any] struct {
	key *localKey
}

type localKey struct{ name string }

func (k *localKey) String() string { return "app.Local(" + k.name + ")" }

// NewLocal returns a new local; name appears only in debugging output.
func NewLocal[T any](name string) Local[T] {
	return Local[T]{key: &localKey{name: name}}
}

// With returns r with v as the value of the local.
func (l Local[T]) With(r *http.Request, v T) *http.Request {
	return r.WithContext(l.WithContext(r.Context(), v))
}

// WithContext returns ctx with v as the value of the local.
func (l Local[T]) WithContext(ctx context.Context, v T) context.Context {
	return context.WithValue(ctx, l.key, v)
}

// Get returns the value of the local in ctx and whether it was set. A
// response that read a local depends on the user: it stays private and
// serves one navigation from the prefetch cache (G-4).
func (l Local[T]) Get(ctx context.Context) (T, bool) {
	markUser(ctx)
	v, ok := ctx.Value(l.key).(T)
	return v, ok
}

// requestState is what the handler tracks for one request across its
// guards, loaders and actions, which may run concurrently.
type requestState struct {
	mu       sync.Mutex
	user     bool
	cookies  []*http.Cookie
	prefetch bool
	dev      bool
}

type stateKey struct{}

func stateOf(ctx context.Context) *requestState {
	if ctx == nil {
		return nil
	}
	st, _ := ctx.Value(stateKey{}).(*requestState)
	return st
}

// markUser marks the response of the request in ctx as user-dependent.
func markUser(ctx context.Context) {
	if st := stateOf(ctx); st != nil {
		st.mu.Lock()
		st.user = true
		st.mu.Unlock()
	}
}

// withState puts a new request state into r.
func (h *Handler) withState(r *http.Request) (*http.Request, *requestState) {
	st := &requestState{prefetch: protocol.IsPrefetch(r.Header), dev: h.dev}
	return r.WithContext(context.WithValue(r.Context(), stateKey{}, st)), st
}

// Cookie returns the named cookie of the request. Reading a cookie marks
// the response as user-dependent (G-4).
func (r *Request) Cookie(name string) (*http.Cookie, error) {
	if r.HTTP == nil {
		return nil, http.ErrNoCookie
	}
	markUser(r.HTTP.Context())
	return r.HTTP.Cookie(name)
}

// SetCookie adds a Set-Cookie header to the response, also to a JSON
// navigation response (G-4). The cookie gets the defaults of a session
// cookie: Path "/" when empty, SameSite Lax when unset, HttpOnly, and
// Secure outside Config.Dev. The response of a prefetch never sets
// cookies. Setting a cookie marks the response as user-dependent.
func (r *Request) SetCookie(c *http.Cookie) {
	if c == nil || r.HTTP == nil {
		return
	}
	st := stateOf(r.HTTP.Context())
	if st == nil {
		return
	}
	cookie := *c
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	if cookie.SameSite == 0 {
		cookie.SameSite = http.SameSiteLaxMode
	}
	cookie.HttpOnly = true
	if !st.dev {
		cookie.Secure = true
	}
	st.mu.Lock()
	st.user = true
	st.cookies = append(st.cookies, &cookie)
	st.mu.Unlock()
}

// finish applies the state of the request to the response headers before
// they are written: its cookies (not for a prefetch) and, for a
// user-dependent response, Cache-Control private instead of public. It
// reports whether the response is user-dependent.
func (h *Handler) finish(w http.ResponseWriter, r *http.Request) bool {
	st := stateOf(r.Context())
	if st == nil {
		return false
	}
	st.mu.Lock()
	user, cookies := st.user, st.cookies
	st.mu.Unlock()
	if !st.prefetch {
		for _, c := range cookies {
			if v := c.String(); v != "" {
				w.Header().Add("Set-Cookie", v)
			}
		}
	}
	if user {
		if cc := w.Header().Get("Cache-Control"); strings.Contains(strings.ToLower(cc), "public") {
			if h.dev {
				h.logger.Warn("vela app: a user-dependent response is private, not public; it read a cookie or an app.Local", "path", r.URL.Path, "cache-control", cc)
			}
			w.Header().Set("Cache-Control", "private")
		}
	}
	return user
}
