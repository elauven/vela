package app

import (
	"net/http"
	"strconv"

	"github.com/elauven/vela/app/internal/protocol"
)

// Helpers for net/http middleware in front of the handler (G-6). A
// navigation request of the client router expects a JSON answer; a plain
// http.Redirect or http.Error would make it load the document.

// IsNavigation reports a navigation request of the client router
// (X-Vela-Navigate), which expects a JSON answer.
func IsNavigation(r *http.Request) bool {
	return len(r.Header.Values(protocol.HeaderNavigate)) > 0
}

// WriteRedirect redirects from middleware. A navigation request gets
// {type: "redirect"}, which the router follows without loading a document;
// a document request gets the status (301, 302, 303, 307 or 308; 303 when
// another one is given) and the Location header.
func WriteRedirect(w http.ResponseWriter, r *http.Request, code int, url string) {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		code = http.StatusSeeOther
	}
	w.Header().Set("Vary", protocol.Vary)
	if !IsNavigation(r) {
		w.Header().Set("Cache-Control", "private")
		http.Redirect(w, r, url, code)
		return
	}
	writeMiddlewareJSON(w, r, protocol.Response{Type: protocol.TypeRedirect, Location: url, Status: code})
}

// WriteError answers from middleware with an error, such as 429 or 503. A
// navigation request gets {type: "reload"}: the router loads the document,
// which gets the same answer as plain text. Middleware has no route chain
// for Error.vela; an error page with the layouts is the job of a guard.
func WriteError(w http.ResponseWriter, r *http.Request, status int, message string) {
	w.Header().Set("Vary", protocol.Vary)
	if !IsNavigation(r) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, message, status)
		return
	}
	writeMiddlewareJSON(w, r, protocol.Response{Type: protocol.TypeReload, Reason: protocol.ReasonDocument})
}

// writeMiddlewareJSON writes a navigation response without a handler. It
// carries the build id of the request, so the router never takes it for an
// answer of another build.
func writeMiddlewareJSON(w http.ResponseWriter, r *http.Request, resp protocol.Response) {
	resp.Build = r.Header.Get(protocol.HeaderBuild)
	if resp.Build == "" {
		resp.Build = "-"
	}
	body, err := protocol.Encode(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
