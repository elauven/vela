// Package users holds the loaders of src/routes/users.
package users

import (
	"context"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

type Summary struct {
	Count int          `json:"count"`
	Users []store.User `json:"users"`
}

// Layout loads the sidebar of every users page. A navigation between users
// keeps the sidebar, so this loader does not run again (docs/APP.md N-4).
func Layout(ctx context.Context, r *app.Request) (Summary, error) {
	store.CountLoad("users/Layout")
	users := store.Users()
	return Summary{Count: len(users), Users: users}, nil
}

type List struct {
	Users []store.User `json:"users"`
}

// Page lists the users.
func Page(ctx context.Context, r *app.Request) (List, error) {
	r.Head().Title = "Users"
	return List{Users: store.Users()}, nil
}
