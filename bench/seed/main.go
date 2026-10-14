// Command seed fills the benchmark database deterministically and writes the
// render fixture (task 058). The same flags always produce the same rows, so
// both applications are measured on identical data.
//
//	go run ./seed -dsn postgres://bench:bench@localhost/bench
//	go run ./seed -fixture shared/fixtures/board.json -fixture-only
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	statuses   = []string{"backlog", "todo", "in_progress", "done"}
	firstNames = []string{"Ada", "Alan", "Grace", "Linus", "Barbara", "Ken", "Margaret", "Dennis", "Radia", "Edsger", "Frances", "John", "Katherine", "Donald", "Sophie", "Niklaus", "Hedy", "Bjarne", "Annie", "Guido"}
	lastNames  = []string{"Lovelace", "Turing", "Hopper", "Torvalds", "Liskov", "Thompson", "Hamilton", "Ritchie", "Perlman", "Dijkstra", "Allen", "Backus", "Johnson", "Knuth", "Wilson", "Wirth", "Lamarr", "Stroustrup", "Easley", "Rossum"}
	verbs      = []string{"Fix", "Add", "Refactor", "Remove", "Improve", "Document", "Investigate", "Migrate", "Optimise", "Test", "Redesign", "Validate"}
	objects    = []string{"login form", "search index", "billing page", "API rate limiter", "image uploads", "dark theme", "export to CSV", "session handling", "notification emails", "audit log", "dashboard charts", "user settings", "webhook retries", "cache layer", "onboarding flow", "access control", "file previews", "keyboard shortcuts", "mobile layout", "error reporting"}
	details    = []string{"on Safari", "for large accounts", "after the last release", "in the admin panel", "behind the feature flag", "for new users", "under load", "in production", "on slow networks", "with SSO enabled"}
	words      = strings.Fields("the a we should could this that it when after before because users page request response error value data list item field form button table cache server client query index file upload download label status priority comment review release deploy test build merge branch issue project team")
	labelNames = []string{"bug", "feature", "frontend", "backend", "design", "docs", "performance", "security", "ux", "infra"}
	colors     = []string{"#ef4444", "#f97316", "#eab308", "#22c55e", "#14b8a6", "#3b82f6", "#6366f1", "#a855f7", "#ec4899", "#64748b"}
	projectDef = []struct{ key, name string }{{"WEB", "Website"}, {"API", "Public API"}, {"MOB", "Mobile app"}, {"OPS", "Operations"}, {"DES", "Design system"}, {"BIL", "Billing"}, {"SRC", "Search"}, {"NTF", "Notifications"}, {"ANL", "Analytics"}, {"SEC", "Security"}}
)

// epoch is the fixed "now" of the data set.
var epoch = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func main() {
	dsn := flag.String("dsn", envOr("DATABASE_URL", "postgres://bench:bench@localhost:5432/bench"), "PostgreSQL connection string")
	schema := flag.String("schema", "shared/schema.sql", "schema to apply first")
	projects := flag.Int("projects", 20, "number of projects")
	users := flag.Int("users", 50, "number of users")
	issues := flag.Int("issues", 50000, "number of issues")
	comments := flag.Int("comments", 200000, "number of comments")
	fixture := flag.String("fixture", "shared/fixtures/board.json", "render fixture to write (empty: none)")
	fixtureCards := flag.Int("fixture-cards", 500, "cards in the render fixture")
	fixtureOnly := flag.Bool("fixture-only", false, "write the fixture without touching the database")
	flag.Parse()

	if *fixture != "" {
		if err := writeFixture(*fixture, *fixtureCards); err != nil {
			log.Fatal(err)
		}
		log.Printf("fixture %s: %d cards", *fixture, *fixtureCards)
	}
	if *fixtureOnly {
		return
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(ctx)
	sql, err := os.ReadFile(*schema)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(sql)); err != nil {
		log.Fatalf("schema: %v", err)
	}
	start := time.Now()
	if err := seed(ctx, conn, *projects, *users, *issues, *comments); err != nil {
		log.Fatal(err)
	}
	log.Printf("seeded %d projects, %d users, %d issues, %d comments in %s", *projects, *users, *issues, *comments, time.Since(start).Round(time.Millisecond))
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func newRand() *rand.Rand { return rand.New(rand.NewPCG(58, 2026)) }

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

func title(r *rand.Rand) string {
	t := pick(r, verbs) + " " + pick(r, objects)
	if r.IntN(3) == 0 {
		t += " " + pick(r, details)
	}
	return t
}

func sentence(r *rand.Rand, n int) string {
	ws := make([]string, n)
	for i := range ws {
		ws[i] = pick(r, words)
	}
	s := strings.Join(ws, " ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func paragraph(r *rand.Rand) string {
	n := 1 + r.IntN(4)
	parts := make([]string, n)
	for i := range parts {
		parts[i] = sentence(r, 6+r.IntN(10))
	}
	return strings.Join(parts, " ")
}

func seed(ctx context.Context, conn *pgx.Conn, nProjects, nUsers, nIssues, nComments int) error {
	r := newRand()
	copyRows := func(table string, cols []string, rows [][]any) error {
		_, err := conn.CopyFrom(ctx, pgx.Identifier{table}, cols, pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		return nil
	}

	var rows [][]any
	for i := 1; i <= nUsers; i++ {
		first, last := firstNames[(i-1)%len(firstNames)], lastNames[((i-1)/len(firstNames)+i-1)%len(lastNames)]
		rows = append(rows, []any{i, first + " " + last, fmt.Sprintf("user%d@example.com", i), colors[i%len(colors)]})
	}
	if err := copyRows("users", []string{"id", "name", "email", "color"}, rows); err != nil {
		return err
	}

	rows = nil
	var labelRows [][]any
	labelsByProject := map[int][]int{}
	labelID := 0
	for p := 1; p <= nProjects; p++ {
		def := projectDef[(p-1)%len(projectDef)]
		key, name := def.key, def.name
		if p > len(projectDef) {
			key, name = fmt.Sprintf("%s%d", def.key, (p-1)/len(projectDef)+1), fmt.Sprintf("%s %d", def.name, (p-1)/len(projectDef)+1)
		}
		rows = append(rows, []any{p, key, name, paragraph(r)})
		for l, ln := range labelNames {
			labelID++
			labelRows = append(labelRows, []any{labelID, p, ln, colors[l]})
			labelsByProject[p] = append(labelsByProject[p], labelID)
		}
	}
	if err := copyRows("projects", []string{"id", "key", "name", "description"}, rows); err != nil {
		return err
	}
	if err := copyRows("labels", []string{"id", "project_id", "name", "color"}, labelRows); err != nil {
		return err
	}

	rows = nil
	var issueLabels [][]any
	numbers := map[int]int{}
	positions := map[[2]int]float64{}
	for i := 1; i <= nIssues; i++ {
		p := 1 + r.IntN(nProjects)
		numbers[p]++
		status := r.IntN(len(statuses))
		positions[[2]int{p, status}] += 1024
		created := epoch.Add(-time.Duration(r.IntN(365*24)) * time.Hour)
		updated := created.Add(time.Duration(r.IntN(int(epoch.Sub(created).Hours())+1)) * time.Hour)
		var assignee, due any
		if r.IntN(5) != 0 {
			assignee = 1 + r.IntN(nUsers)
		}
		if r.IntN(3) == 0 {
			due = epoch.AddDate(0, 0, r.IntN(90)-30)
		}
		rows = append(rows, []any{int64(i), p, numbers[p], title(r), paragraph(r), statuses[status], int16(r.IntN(4)), assignee, positions[[2]int{p, status}], due, created, updated})
		ls := labelsByProject[p]
		for _, k := range r.Perm(len(ls))[:r.IntN(4)] {
			issueLabels = append(issueLabels, []any{int64(i), ls[k]})
		}
	}
	if err := copyRows("issues", []string{"id", "project_id", "number", "title", "description", "status", "priority", "assignee_id", "position", "due_date", "created_at", "updated_at"}, rows); err != nil {
		return err
	}
	if err := copyRows("issue_labels", []string{"issue_id", "label_id"}, issueLabels); err != nil {
		return err
	}

	rows = nil
	for i := 1; i <= nComments; i++ {
		rows = append(rows, []any{int64(i), int64(1 + r.IntN(nIssues)), 1 + r.IntN(nUsers), paragraph(r), epoch.Add(-time.Duration(r.IntN(365*24*60)) * time.Minute)})
	}
	if err := copyRows("comments", []string{"id", "issue_id", "author_id", "body", "created_at"}, rows); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "UPDATE projects p SET next_number = coalesce((SELECT max(number) FROM issues i WHERE i.project_id = p.id), 0) + 1"); err != nil {
		return err
	}
	for _, seq := range []string{"issues", "comments"} {
		if _, err := conn.Exec(ctx, fmt.Sprintf("SELECT setval('%s_id_seq', (SELECT max(id) FROM %s))", seq, seq)); err != nil {
			return err
		}
	}
	_, err := conn.Exec(ctx, "ANALYZE")
	return err
}

// FixtureCard is one card of the render fixture; the applications build
// board columns from the first n cards in order (bench/README.md).
type FixtureCard struct {
	ID          int64          `json:"id"`
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	Status      string         `json:"status"`
	Priority    int            `json:"priority"`
	Assignee    *FixtureUser   `json:"assignee"`
	Labels      []FixtureLabel `json:"labels"`
	Due         string         `json:"due"`
	Comments    int            `json:"comments"`
	Attachments int            `json:"attachments"`
}

type FixtureUser struct {
	Name     string `json:"name"`
	Initials string `json:"initials"`
	Color    string `json:"color"`
}

type FixtureLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

func writeFixture(path string, n int) error {
	r := rand.New(rand.NewPCG(7, 500))
	cards := make([]FixtureCard, n)
	for i := range cards {
		c := FixtureCard{ID: int64(i + 1), Key: fmt.Sprintf("WEB-%d", i+1), Title: title(r), Status: statuses[i%len(statuses)], Priority: r.IntN(4), Labels: []FixtureLabel{}, Comments: r.IntN(12), Attachments: r.IntN(3)}
		if r.IntN(5) != 0 {
			first, last := pick(r, firstNames), pick(r, lastNames)
			c.Assignee = &FixtureUser{Name: first + " " + last, Initials: first[:1] + last[:1], Color: pick(r, colors)}
		}
		for _, k := range r.Perm(len(labelNames))[:r.IntN(4)] {
			c.Labels = append(c.Labels, FixtureLabel{Name: labelNames[k], Color: colors[k]})
		}
		if r.IntN(3) == 0 {
			c.Due = epoch.AddDate(0, 0, r.IntN(90)-30).Format("Jan 2")
		}
		cards[i] = c
	}
	data, err := json.MarshalIndent(cards, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
