// Package users holds the loaders of src/routes/users.
package users

import (
	"context"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

type Summary struct {
	Count int `json:"count"`
}

// Layout loads the user count shown next to every users page.
func Layout(ctx context.Context, r *app.Request) (Summary, error) {
	return Summary{Count: len(store.Users())}, nil
}

type List struct {
	Users []store.User `json:"users"`
}

// Page lists the users.
func Page(ctx context.Context, r *app.Request) (List, error) {
	return List{Users: store.Users()}, nil
}
