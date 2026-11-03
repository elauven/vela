package routes

import (
	"context"

	"github.com/elauven/vela/app"
)

type Site struct {
	Title string `json:"title"`
}

func Layout(ctx context.Context, r *app.Request) (Site, error) {
	return Site{Title: "Golden"}, nil
}
