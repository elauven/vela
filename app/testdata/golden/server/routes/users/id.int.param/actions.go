package id_int_param

import (
	"context"

	"github.com/elauven/vela/app"
)

func Actions() app.Actions {
	return app.Actions{"default": save}
}

func save(ctx context.Context, r *app.Request) (any, error) {
	return nil, nil
}
