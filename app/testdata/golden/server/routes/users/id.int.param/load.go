package id_int_param

import (
	"context"

	"github.com/elauven/vela/app"
)

const Render = app.RenderFragment

type User struct {
	Name string `json:"name"`
}

func Page(ctx context.Context, r *app.Request) (User, error) {
	r.Head().Title = "Ada"
	return User{Name: "Ada"}, nil
}
