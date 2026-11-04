// Package store is the example's data.
package store

import "sync"

type User struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Joined string `json:"joined"`
}

var (
	mu     sync.Mutex
	loads  = map[string]int{}
	users  = []User{{ID: 1, Name: "Ada", Joined: "2026-01-04"}, {ID: 2, Name: "Grace", Joined: "2026-03-17"}}
	nextID = 3
)

// Users returns the users.
func Users() []User {
	mu.Lock()
	defer mu.Unlock()
	return append([]User(nil), users...)
}

// AddUser adds a user and returns it.
func AddUser(name string) User {
	mu.Lock()
	defer mu.Unlock()
	user := User{ID: nextID, Name: name, Joined: "2026-11-04"}
	nextID++
	users = append(users, user)
	return user
}

// DeleteUser removes a user; it reports whether there was one.
func DeleteUser(id int) bool {
	mu.Lock()
	defer mu.Unlock()
	for i, user := range users {
		if user.ID == id {
			users = append(users[:i], users[i+1:]...)
			return true
		}
	}
	return false
}

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

type DocLink struct {
	Href  string `json:"href"`
	Title string `json:"title"`
}

type Doc struct {
	Title      string   `json:"title"`
	Paragraphs []string `json:"paragraphs"`
}

var docs = map[string]Doc{
	"intro":   {Title: "Introduction", Paragraphs: []string{"Vela App renders pages on a Go server.", "The docs are static: they need no JavaScript."}},
	"routing": {Title: "Routing", Paragraphs: []string{"Routes come from the files of src/routes."}},
}

// DocLinks is the table of contents of the docs.
func DocLinks() []DocLink {
	return []DocLink{{Href: "/docs/intro", Title: "Introduction"}, {Href: "/docs/routing", Title: "Routing"}}
}

// FindDoc returns the page of the docs at path.
func FindDoc(path string) (Doc, bool) {
	doc, ok := docs[path]
	return doc, ok
}

type Row struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

type Report struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Rows []Row  `json:"rows"`
}

// Reports are the reports of the example.
func Reports() []Report {
	return []Report{
		{ID: 1, Name: "Sales", Rows: []Row{{"North", 30}, {"South", 10}, {"East", 20}}},
		{ID: 2, Name: "Visits", Rows: []Row{{"Mon", 5}, {"Tue", 9}, {"Wed", 7}}},
	}
}
