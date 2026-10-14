package main

// Queries and view models of bench/README.md. Page data is built as
// *vela.Object, the object type of the Vela Go runtime: generated renderers
// read it without conversion, and it marshals to the same JSON for the API.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	vela "github.com/elauven/vela/ssr_runtime"
)

var statusOrder = []string{"backlog", "todo", "in_progress", "done"}

var statusTitles = map[string]string{"backlog": "Backlog", "todo": "To do", "in_progress": "In progress", "done": "Done"}

var priorityTitles = []string{"None", "Low", "High", "Urgent"}

const pageSize = 50

func shortDate(t time.Time) string { return t.UTC().Format("Jan 2") }

func longDate(t time.Time) string { return t.UTC().Format("Jan 2, 2006") }

func dueLabel(t *time.Time) string {
	if t == nil {
		return ""
	}
	return shortDate(*t)
}

// sizeLabel rounds half up, as JavaScript's toFixed(1) does.
func sizeLabel(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", math.Floor(float64(n)/1024*10+0.5)/10)
	default:
		return fmt.Sprintf("%.1f MB", math.Floor(float64(n)/1024/1024*10+0.5)/10)
	}
}

func initials(name string) string {
	var out []rune
	for _, word := range strings.Fields(name) {
		r, _ := utf8.DecodeRuneInString(word)
		out = append(out, unicode.ToUpper(r))
	}
	if len(out) > 2 {
		out = []rune{out[0], out[len(out)-1]}
	}
	return string(out)
}

func user(name, color *string) any {
	if name == nil {
		return nil
	}
	return vela.NewObject().Set("name", *name).Set("initials", initials(*name)).Set("color", *color)
}

func labels(names, colors []string) []any {
	out := make([]any, len(names))
	for i := range names {
		out[i] = vela.NewObject().Set("name", names[i]).Set("color", colors[i])
	}
	return out
}

// ── Board ──────────────────────────────────────────────────────────────────

const boardSQL = `
SELECT i.id, p.key || '-' || i.number, i.title, i.status, i.priority, i.due_date, u.name, u.color,
       (SELECT count(*) FROM comments c WHERE c.issue_id = i.id),
       (SELECT count(*) FROM attachments a WHERE a.issue_id = i.id),
       ARRAY(SELECT l.name FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id ORDER BY l.name),
       ARRAY(SELECT l.color FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id ORDER BY l.name)
FROM (SELECT *, row_number() OVER (PARTITION BY status ORDER BY position) AS rn
      FROM issues WHERE project_id = $1) i
JOIN projects p ON p.id = i.project_id
LEFT JOIN users u ON u.id = i.assignee_id
WHERE i.rn <= 100
ORDER BY i.position`

func (a *app) board(ctx context.Context, projectID int) (project any, columns, assignees []any, err error) {
	var key, name string
	if err = a.db.QueryRow(ctx, `SELECT key, name FROM projects WHERE id = $1`, projectID).Scan(&key, &name); err != nil {
		return
	}
	project = vela.NewObject().Set("id", projectID).Set("key", key).Set("name", name)
	rows, err := a.db.Query(ctx, boardSQL, projectID)
	if err != nil {
		return
	}
	defer rows.Close()
	var cards []any
	for rows.Next() {
		var (
			id                 int64
			key, title, status string
			priority           int16
			due                *time.Time
			uname, ucolor      *string
			ncomments, nfiles  int64
			lnames, lcolors    []string
		)
		if err = rows.Scan(&id, &key, &title, &status, &priority, &due, &uname, &ucolor, &ncomments, &nfiles, &lnames, &lcolors); err != nil {
			return
		}
		cards = append(cards, vela.NewObject().Set("id", id).Set("key", key).Set("title", title).Set("status", status).
			Set("priority", int64(priority)).Set("assignee", user(uname, ucolor)).Set("labels", labels(lnames, lcolors)).
			Set("due", dueLabel(due)).Set("comments", ncomments).Set("attachments", nfiles))
	}
	if err = rows.Err(); err != nil {
		return
	}
	columns, assignees = groupColumns(cards)
	return
}

// groupColumns builds the four board columns from cards in order and the
// sorted distinct assignee names.
func groupColumns(cards []any) (columns, assignees []any) {
	byStatus := map[string][]any{}
	names := map[string]bool{}
	for _, c := range cards {
		card := c.(*vela.Object)
		status, _ := card.Lookup("status")
		byStatus[status.(string)] = append(byStatus[status.(string)], card)
		if u, _ := card.Lookup("assignee"); u != nil {
			name, _ := u.(*vela.Object).Lookup("name")
			names[name.(string)] = true
		}
	}
	for _, s := range statusOrder {
		list := byStatus[s]
		if list == nil {
			list = []any{}
		}
		columns = append(columns, vela.NewObject().Set("status", s).Set("title", statusTitles[s]).Set("cards", list))
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	assignees = make([]any, len(sorted))
	for i, n := range sorted {
		assignees[i] = n
	}
	return
}

var fixtureProject = vela.NewObject().Set("id", 0).Set("key", "WEB").Set("name", "Render fixture")

type fixtureCard struct {
	ID          int64  `json:"id"`
	Key         string `json:"key"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Priority    int64  `json:"priority"`
	Assignee    *struct{ Name, Initials, Color string }
	Labels      []struct{ Name, Color string }
	Due         string `json:"due"`
	Comments    int64  `json:"comments"`
	Attachments int64  `json:"attachments"`
}

func loadFixture(path string) ([]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cards []fixtureCard
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, err
	}
	out := make([]any, len(cards))
	for i, c := range cards {
		var assignee any
		if c.Assignee != nil {
			assignee = vela.NewObject().Set("name", c.Assignee.Name).Set("initials", c.Assignee.Initials).Set("color", c.Assignee.Color)
		}
		ls := make([]any, len(c.Labels))
		for j, l := range c.Labels {
			ls[j] = vela.NewObject().Set("name", l.Name).Set("color", l.Color)
		}
		out[i] = vela.NewObject().Set("id", c.ID).Set("key", c.Key).Set("title", c.Title).Set("status", c.Status).
			Set("priority", c.Priority).Set("assignee", assignee).Set("labels", ls).Set("due", c.Due).
			Set("comments", c.Comments).Set("attachments", c.Attachments)
	}
	return out, nil
}

// ── Issue list ─────────────────────────────────────────────────────────────

type filters struct {
	project       int
	status, q     string
	sort          string
	page          int
	projectString string
}

func parseFilters(r *http.Request) filters {
	q := r.URL.Query()
	f := filters{project: 1, status: q.Get("status"), q: strings.TrimSpace(q.Get("q")), sort: q.Get("sort"), page: 1}
	if v, err := strconv.Atoi(q.Get("project")); err == nil && v > 0 {
		f.project = v
	}
	if _, ok := statusTitles[f.status]; !ok {
		f.status = ""
	}
	if f.sort != "priority" && f.sort != "number" {
		f.sort = "updated"
	}
	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		f.page = v
	}
	f.projectString = strconv.Itoa(f.project)
	return f
}

func (f filters) object() *vela.Object {
	return vela.NewObject().Set("project", f.projectString).Set("status", f.status).Set("q", f.q).Set("sort", f.sort).Set("page", f.page)
}

var listOrder = map[string]string{
	"updated":  "i.updated_at DESC, i.id DESC",
	"priority": "i.priority DESC, i.updated_at DESC, i.id DESC",
	"number":   "i.number ASC",
}

func likePattern(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
}

func (a *app) issueList(ctx context.Context, f filters) (rows []any, total, pages int, err error) {
	where := `i.project_id = $1 AND ($2 = '' OR i.status = $2) AND ($3 = '' OR i.title ILIKE $4)`
	args := []any{f.project, f.status, f.q, likePattern(f.q)}
	if err = a.db.QueryRow(ctx, `SELECT count(*) FROM issues i WHERE `+where, args...).Scan(&total); err != nil {
		return
	}
	pages = max(1, (total+pageSize-1)/pageSize)
	query := `SELECT i.id, p.key || '-' || i.number, i.title, i.status, i.priority, u.name, u.color, i.updated_at,
	                 (SELECT count(*) FROM comments c WHERE c.issue_id = i.id)
	          FROM issues i JOIN projects p ON p.id = i.project_id LEFT JOIN users u ON u.id = i.assignee_id
	          WHERE ` + where + ` ORDER BY ` + listOrder[f.sort] + ` LIMIT 50 OFFSET $5`
	result, err := a.db.Query(ctx, query, append(args, (f.page-1)*pageSize)...)
	if err != nil {
		return
	}
	defer result.Close()
	rows = []any{}
	for result.Next() {
		var (
			id                 int64
			key, title, status string
			priority           int16
			uname, ucolor      *string
			updated            time.Time
			ncomments          int64
		)
		if err = result.Scan(&id, &key, &title, &status, &priority, &uname, &ucolor, &updated, &ncomments); err != nil {
			return
		}
		rows = append(rows, vela.NewObject().Set("id", id).Set("key", key).Set("title", title).Set("status", status).
			Set("statusTitle", statusTitles[status]).Set("priority", int64(priority)).Set("assignee", user(uname, ucolor)).
			Set("updated", shortDate(updated)).Set("comments", ncomments))
	}
	err = result.Err()
	return
}

func (a *app) projects(ctx context.Context) ([]any, error) {
	rows, err := a.db.Query(ctx, `SELECT id, key, name FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []any
	for rows.Next() {
		var id int64
		var key, name string
		if err := rows.Scan(&id, &key, &name); err != nil {
			return nil, err
		}
		out = append(out, vela.NewObject().Set("id", id).Set("key", key).Set("name", name))
	}
	return out, rows.Err()
}

// ── Issue page ─────────────────────────────────────────────────────────────

func (a *app) issue(ctx context.Context, id int64) (issue any, comments, attachments []any, err error) {
	var (
		number                      int64
		title, description, status  string
		priority                    int16
		due                         *time.Time
		created, updated            time.Time
		uname, ucolor               *string
		pid                         int64
		pkey, pname                 string
		lnames, lcolors             []string
	)
	err = a.db.QueryRow(ctx, `
SELECT i.number, i.title, i.description, i.status, i.priority, i.due_date, i.created_at, i.updated_at, u.name, u.color, p.id, p.key, p.name,
       ARRAY(SELECT l.name FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id ORDER BY l.name),
       ARRAY(SELECT l.color FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id ORDER BY l.name)
FROM issues i JOIN projects p ON p.id = i.project_id LEFT JOIN users u ON u.id = i.assignee_id
WHERE i.id = $1`, id).Scan(&number, &title, &description, &status, &priority, &due, &created, &updated, &uname, &ucolor, &pid, &pkey, &pname, &lnames, &lcolors)
	if err != nil {
		return
	}
	issue = vela.NewObject().Set("id", id).Set("key", fmt.Sprintf("%s-%d", pkey, number)).Set("title", title).
		Set("description", description).Set("status", status).Set("priority", int64(priority)).Set("assignee", user(uname, ucolor)).
		Set("labels", labels(lnames, lcolors)).Set("project", vela.NewObject().Set("id", pid).Set("key", pkey).Set("name", pname)).
		Set("due", dueLabel(due)).Set("created", longDate(created)).Set("updated", longDate(updated))

	rows, err := a.db.Query(ctx, `SELECT c.id, u.name, u.color, c.body, c.created_at FROM comments c JOIN users u ON u.id = c.author_id
	                              WHERE c.issue_id = $1 ORDER BY c.created_at, c.id`, id)
	if err != nil {
		return
	}
	comments = []any{}
	for rows.Next() {
		var cid int64
		var name, color, body string
		var at time.Time
		if err = rows.Scan(&cid, &name, &color, &body, &at); err != nil {
			rows.Close()
			return
		}
		comments = append(comments, commentObject(cid, name, color, body, at))
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return
	}
	rows, err = a.db.Query(ctx, `SELECT id, filename, size, created_at FROM attachments WHERE issue_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return
	}
	defer rows.Close()
	attachments = []any{}
	for rows.Next() {
		var aid, size int64
		var filename string
		var at time.Time
		if err = rows.Scan(&aid, &filename, &size, &at); err != nil {
			return
		}
		attachments = append(attachments, attachmentObject(aid, filename, size, at))
	}
	err = rows.Err()
	return
}

func commentObject(id int64, name, color, body string, at time.Time) *vela.Object {
	return vela.NewObject().Set("id", id).Set("author", user(&name, &color)).Set("body", body).Set("created", longDate(at))
}

func attachmentObject(id int64, filename string, size int64, at time.Time) *vela.Object {
	return vela.NewObject().Set("id", id).Set("filename", filename).Set("size", size).Set("sizeLabel", sizeLabel(size)).
		Set("created", longDate(at)).Set("url", "/files/"+strconv.FormatInt(id, 10))
}

// ── Dashboard ──────────────────────────────────────────────────────────────

type dashboardData struct {
	totals                                  *vela.Object
	byStatus, byPriority, projects, recent []any
}

func pct(n, total int64) int64 {
	if total == 0 {
		return 0
	}
	return int64(math.Floor(float64(n)*100/float64(total) + 0.5))
}

func (a *app) dashboard(ctx context.Context) (d dashboardData, err error) {
	var issues, open, done, comments int64
	if err = a.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status <> 'done'), count(*) FILTER (WHERE status = 'done'),
	                             (SELECT count(*) FROM comments) FROM issues`).Scan(&issues, &open, &done, &comments); err != nil {
		return
	}
	d.totals = vela.NewObject().Set("issues", issues).Set("open", open).Set("done", done).Set("comments", comments)

	counts := map[string]int64{}
	rows, err := a.db.Query(ctx, `SELECT status, count(*) FROM issues GROUP BY status`)
	if err != nil {
		return
	}
	for rows.Next() {
		var s string
		var n int64
		if err = rows.Scan(&s, &n); err != nil {
			rows.Close()
			return
		}
		counts[s] = n
	}
	rows.Close()
	for _, s := range statusOrder {
		d.byStatus = append(d.byStatus, vela.NewObject().Set("status", s).Set("title", statusTitles[s]).Set("count", counts[s]).Set("pct", pct(counts[s], issues)))
	}

	byPriority := make([]int64, 4)
	rows, err = a.db.Query(ctx, `SELECT priority, count(*) FROM issues GROUP BY priority`)
	if err != nil {
		return
	}
	for rows.Next() {
		var p int16
		var n int64
		if err = rows.Scan(&p, &n); err != nil {
			rows.Close()
			return
		}
		byPriority[p] = n
	}
	rows.Close()
	for p := 3; p >= 0; p-- {
		d.byPriority = append(d.byPriority, vela.NewObject().Set("priority", int64(p)).Set("title", priorityTitles[p]).Set("count", byPriority[p]).Set("pct", pct(byPriority[p], issues)))
	}

	rows, err = a.db.Query(ctx, `SELECT p.id, p.key, p.name, count(*) FILTER (WHERE i.status <> 'done'), count(*) FILTER (WHERE i.status = 'done')
	                             FROM projects p LEFT JOIN issues i ON i.project_id = p.id GROUP BY p.id ORDER BY p.id`)
	if err != nil {
		return
	}
	for rows.Next() {
		var id, o, dn int64
		var key, name string
		if err = rows.Scan(&id, &key, &name, &o, &dn); err != nil {
			rows.Close()
			return
		}
		d.projects = append(d.projects, vela.NewObject().Set("id", id).Set("key", key).Set("name", name).Set("open", o).Set("done", dn))
	}
	rows.Close()

	rows, err = a.db.Query(ctx, `SELECT i.id, p.key || '-' || i.number, i.title, i.updated_at FROM issues i JOIN projects p ON p.id = i.project_id
	                             ORDER BY i.updated_at DESC, i.id DESC LIMIT 20`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var key, title string
		var at time.Time
		if err = rows.Scan(&id, &key, &title, &at); err != nil {
			return
		}
		d.recent = append(d.recent, vela.NewObject().Set("id", id).Set("key", key).Set("title", title).Set("updated", shortDate(at)))
	}
	err = rows.Err()
	return
}
