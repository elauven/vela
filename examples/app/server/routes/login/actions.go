package login

import (
	"context"
	"strings"

	"example.com/site/internal/auth"
	"github.com/elauven/vela/app"
)

// Form is the value of a failed sign-in.
type Form struct {
	Error string `json:"error"`
}

// Actions signs in (default) and out (?/logout).
func Actions() app.Actions {
	return app.Actions{
		"default": func(ctx context.Context, r *app.Request) (any, error) {
			form, err := r.Form()
			if err != nil {
				return nil, err
			}
			name := strings.TrimSpace(form.Get("name"))
			if name == "" {
				return nil, app.Fail(422, Form{Error: "Enter a name."})
			}
			auth.SignIn(r, name)
			return nil, app.Redirect(303, safeNext(form.Get("next")))
		},
		"logout": func(ctx context.Context, r *app.Request) (any, error) {
			auth.SignOut(r)
			return nil, app.Redirect(303, "/")
		},
	}
}
