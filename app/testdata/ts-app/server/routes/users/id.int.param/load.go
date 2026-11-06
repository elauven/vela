// Package id_int_param holds the loaders of src/routes/users/[id=int].
package id_int_param

import (
	"context"
	"time"

	"github.com/elauven/vela/app"
)

type User struct {
	Name   string    `json:"name"`
	Joined time.Time `json:"joined"`
}

type Detail struct {
	User    User     `json:"user"`
	Manager *User    `json:"manager"`
	Tags    []string `json:"tags"`
}

// Form is what the actions pass to app.Fail.
type Form struct {
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// Page loads one user.
func Page(ctx context.Context, r *app.Request) (Detail, error) {
	return Detail{User: User{Name: "Ada", Joined: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}, Tags: []string{"admin"}}, nil
}

// Actions renames the user.
func Actions() app.Actions {
	return app.Actions{
		"default": func(ctx context.Context, r *app.Request) (any, error) {
			return nil, app.Fail(422, Form{Message: "A name is required.", Field: "name"})
		},
	}
}
