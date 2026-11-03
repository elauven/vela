// Package store is the example's data.
package store

type User struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Joined string `json:"joined"`
}

func Users() []User {
	return []User{
		{ID: 1, Name: "Ada", Joined: "2026-01-04"},
		{ID: 2, Name: "Grace", Joined: "2026-03-17"},
	}
}
