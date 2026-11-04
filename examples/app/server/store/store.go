// Package store is the example's data.
package store

import "sync"

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

var (
	mu    sync.Mutex
	loads = map[string]int{}
)

// CountLoad counts a run of the loader name. The example's own /api/loads
// handler reports the counts, so a test can see which loaders a navigation
// ran.
func CountLoad(name string) {
	mu.Lock()
	defer mu.Unlock()
	loads[name]++
}

// Loads returns the counts of CountLoad.
func Loads() map[string]int {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]int, len(loads))
	for k, v := range loads {
		out[k] = v
	}
	return out
}
