// Package login signs users in and out (docs/VELA_APP.md, "Authentication").
package login

import (
	"context"

	"example.com/site/internal/auth"
	"github.com/elauven/vela/app"
)

// Account is the data of the sign-in page.
type Account struct {
	User string `json:"user"`
	Next string `json:"next"`
}

// Page shows who is signed in.
func Page(ctx context.Context, r *app.Request) (Account, error) {
	r.Head().Title = "Account"
	account := Account{Next: safeNext(r.Query.Get("next"))}
	if u, ok := auth.Current.Get(ctx); ok {
		account.User = u.Name
	}
	return account, nil
}

// safeNext keeps a local path, so the redirect after sign-in stays on the
// site.
func safeNext(next string) string {
	if len(next) > 1 && next[0] == '/' && next[1] != '/' && next[1] != '\\' {
		return next
	}
	return "/"
}
