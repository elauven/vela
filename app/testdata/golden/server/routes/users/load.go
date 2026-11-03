package users

import (
	"context"

	"github.com/elauven/vela/app"
)

var Deps = app.Deps{Query: []string{"sort"}}

func Layout(ctx context.Context, r *app.Request) (map[string]int, error) {
	return map[string]int{"count": 2}, nil
}
