package users

import (
	"context"
	"strings"

	"example.com/site/server/store"
	"github.com/elauven/vela/app"
)

// Actions are the forms of the users page (docs/APP.md, form actions):
// POST /users adds a user, POST /users?/delete removes one. They work with
// and without JavaScript.
func Actions() app.Actions {
	return app.Actions{"default": add, "delete": remove}
}

// NewUser is the add form; a failure sends it back with its error.
type NewUser struct {
	Name  string `json:"name" form:"name"`
	Error string `json:"error,omitempty"`
}

func add(ctx context.Context, r *app.Request) (any, error) {
	var in NewUser
	if err := r.Decode(&in); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Error = "A name is required."
		return nil, app.Fail(422, in)
	}
	store.AddUser(in.Name)
	return nil, nil
}

func remove(ctx context.Context, r *app.Request) (any, error) {
	var in struct {
		ID int `form:"id"`
	}
	if err := r.Decode(&in); err != nil {
		return nil, err
	}
	if !store.DeleteUser(in.ID) {
		return nil, app.NotFound()
	}
	return nil, nil
}
