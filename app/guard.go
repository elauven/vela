package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"runtime/debug"
)

// Guards (docs/APP.md G-1 to G-5). Before any loader or action runs, the
// guards of the chain run one after another from the top: for documents,
// data and fragment navigations, prefetches and POSTs, at every level, also
// at levels the client keeps (X-Vela-Have). The first error stops the
// request: a redirect answers at once, any other error renders the
// Error.vela at the guard's level or above, with the levels above it and
// their loaders, and nothing below it runs.

// guardStop is the guard that stopped a request.
type guardStop struct {
	level int
	err   error
}

// runGuards runs the guards of route for r. route is the NotFound chain
// for a URL no route matches; its guards see an empty r.Route.
func (h *Handler) runGuards(r *http.Request, route *Route, params map[string]string) *guardStop {
	if params == nil {
		params = map[string]string{}
	}
	var query url.Values
	for i := range route.Chain {
		guard := route.Chain[i].Guard
		if guard == nil {
			continue
		}
		if query == nil {
			query = r.URL.Query()
		}
		req := &Request{Params: params, Query: query, HTTP: r, Route: route.ID}
		if err := h.mapError(r.Context(), req, callGuard(r.Context(), guard, req)); err != nil {
			return &guardStop{level: i, err: err}
		}
	}
	return nil
}

// callGuard calls a guard; a panic is a *PanicError.
func callGuard(ctx context.Context, guard Guard, req *Request) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &PanicError{Value: v, Stack: debug.Stack()}
		}
	}()
	return guard(ctx, req)
}

// mapError passes an error of a guard, loader, action or render through
// Config.OnError (G-5). Redirects and form failures are answers, not
// errors, and pass unchanged. A panic is logged with its stack first, so
// the stack is in the log whatever OnError returns.
func (h *Handler) mapError(ctx context.Context, r *Request, err error) error {
	if err == nil {
		return nil
	}
	var redirect *RedirectError
	var fail *FailError
	if errors.As(err, &redirect) || errors.As(err, &fail) {
		return err
	}
	var panicked *PanicError
	if errors.As(err, &panicked) && !panicked.logged {
		panicked.logged = true
		attrs := []any{"err", err, "stack", string(panicked.Stack)}
		if r != nil && r.HTTP != nil {
			attrs = append(attrs, "path", r.HTTP.URL.Path, "route", r.Route)
		}
		h.logger.Error("vela app: panic", attrs...)
	}
	if h.onError == nil {
		return err
	}
	mapped := h.callOnError(ctx, r, err)
	if mapped == nil {
		return err
	}
	return mapped
}

// callOnError calls OnError; its own panic keeps the original error.
func (h *Handler) callOnError(ctx context.Context, r *Request, err error) (mapped error) {
	defer func() {
		if v := recover(); v != nil {
			h.logger.Error("vela app: OnError panicked", "err", err, "panic", v, "stack", string(debug.Stack()))
			mapped = nil
		}
	}()
	return h.onError(ctx, r, err)
}
