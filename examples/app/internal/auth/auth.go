// Package auth is the sign-in of the example (docs/VELA_APP.md,
// "Authentication"): a cookie with the user's name, signed with HMAC, which
// Middleware turns into the app.Local User. A real application keeps
// sessions in a store such as github.com/alexedwards/scs.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"os"
	"strings"

	"github.com/elauven/vela/app"
)

// User is the signed-in user.
type User struct {
	Name string `json:"name"`
}

// Current is the user of the request, set by Middleware (docs/APP.md G-3).
var Current = app.NewLocal[*User]("user")

// CookieName is the name of the session cookie.
const CookieName = "session"

func key() []byte {
	if k := os.Getenv("EXAMPLE_SESSION_KEY"); k != "" {
		return []byte(k)
	}
	return []byte("example only: set EXAMPLE_SESSION_KEY")
}

func sign(name string) string {
	mac := hmac.New(sha256.New, key())
	mac.Write([]byte(name))
	return base64.RawURLEncoding.EncodeToString([]byte(name)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verify(value string) (string, bool) {
	encoded, _, ok := strings.Cut(value, ".")
	if !ok {
		return "", false
	}
	name, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !hmac.Equal([]byte(sign(string(name))), []byte(value)) {
		return "", false
	}
	return string(name), true
}

// Middleware puts the user of a valid session cookie into the request.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(CookieName); err == nil {
			if name, ok := verify(c.Value); ok {
				r = Current.With(r, &User{Name: name})
			}
		}
		next.ServeHTTP(w, r)
	})
}

// SignIn sets the session cookie of name on the response of r.
func SignIn(r *app.Request, name string) {
	r.SetCookie(&http.Cookie{Name: CookieName, Value: sign(name)})
}

// SignOut removes the session cookie.
func SignOut(r *app.Request) {
	r.SetCookie(&http.Cookie{Name: CookieName, Value: "", MaxAge: -1})
}
