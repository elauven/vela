// Package routes holds the loaders of src/routes (docs/APP.md L-1).
package routes

import (
	"context"

	"github.com/elauven/vela/app"
)

type MenuItem struct {
	Href  string `json:"href"`
	Label string `json:"label"`
}

type Menu struct {
	Menu []MenuItem `json:"menu"`
}

// Layout loads the menu of the root layout.
func Layout(ctx context.Context, r *app.Request) (Menu, error) {
	return Menu{Menu: []MenuItem{
		{Href: "/", Label: "Home"},
		{Href: "/users", Label: "Users"},
		{Href: "/about", Label: "About"},
		{Href: "/docs/intro", Label: "Docs"},
		{Href: "/reports", Label: "Reports"},
		{Href: "/settings", Label: "Settings"},
		{Href: "/me", Label: "Me"},
	}}, nil
}

// Page loads the home page.
func Page(ctx context.Context, r *app.Request) (map[string]string, error) {
	r.Head().Title = "Home · Vela App"
	return map[string]string{"greeting": "Welcome to Vela App."}, nil
}
