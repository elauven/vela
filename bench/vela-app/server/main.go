// Command server is the Vela + Go version of the benchmark tracker (task 058,
// bench/README.md). Pages are Vela components compiled with vela --ssr: the
// handler loads the data, renders the root component into a buffer, and
// sends one response; the browser hydrates it with the bundled client entry.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pages "github.com/elauven/vela/bench/vela-app/out/server/components/pages"
)

type app struct {
	db      *pgxpool.Pool
	storage string
	fixture []any // render fixture cards as *vela.Object, built once
}

func main() {
	dsn := envOr("DATABASE_URL", "postgres://bench:bench@localhost:5432/bench?sslmode=disable")
	poolSize, _ := strconv.Atoi(envOr("DB_POOL", "20"))
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Fatal(err)
	}
	cfg.MaxConns = int32(poolSize)
	cfg.MinConns = int32(poolSize)
	db, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	here := envOr("APP_DIR", ".")
	a := &app{db: db, storage: envOr("STORAGE_DIR", filepath.Join(here, "..", "storage"))}
	if err := os.MkdirAll(a.storage, 0o755); err != nil {
		log.Fatal(err)
	}
	if a.fixture, err = loadFixture(filepath.Join(here, "..", "shared", "fixtures", "board.json")); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	static := http.FileServer(http.Dir(filepath.Join(here, "static")))
	shared := http.FileServer(http.Dir(filepath.Join(here, "..", "shared", "static")))
	mux.Handle("GET /static/dist/", http.StripPrefix("/static/", static))
	mux.Handle("GET /static/", http.StripPrefix("/static/", shared))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/projects/1/board", http.StatusFound)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("GET /projects/{id}/board", a.boardPage)
	mux.HandleFunc("GET /render/board", a.renderBoard)
	mux.HandleFunc("GET /issues", a.issuesPage)
	mux.HandleFunc("GET /issues/{id}", a.issuePage)
	mux.HandleFunc("GET /dashboard", a.dashboardPage)
	mux.HandleFunc("GET /api/issues", a.apiIssues)
	mux.HandleFunc("POST /api/issues", a.apiCreateIssue)
	mux.HandleFunc("PATCH /api/issues/{id}", a.apiPatchIssue)
	mux.HandleFunc("POST /api/issues/{id}/comments", a.apiComment)
	mux.HandleFunc("POST /api/issues/{id}/attachments", a.apiUpload)
	mux.HandleFunc("GET /files/{id}", a.file)

	// PPROF_ADDR (for example localhost:6060) serves net/http/pprof on a
	// separate listener, for profiling under load; off in measurements.
	if pprofAddr := os.Getenv("PPROF_ADDR"); pprofAddr != "" {
		debug := http.NewServeMux()
		debug.HandleFunc("/debug/pprof/", pprof.Index)
		debug.HandleFunc("/debug/pprof/profile", pprof.Profile)
		debug.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		debug.HandleFunc("/debug/pprof/trace", pprof.Trace)
		go func() { log.Println(http.ListenAndServe(pprofAddr, debug)) }()
	}

	addr := ":" + envOr("PORT", "8081")
	log.Printf("vela-app on %s (GOMAXPROCS=%d, pool=%d)", addr, runtime.GOMAXPROCS(0), poolSize)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// ── Page rendering ─────────────────────────────────────────────────────────

var buffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

const headStart = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>`
const headEnd = ` · Tracker</title><link rel="stylesheet" href="/static/app.css"></head><body><div id="app">`

// page writes the document around one rendered root component. entry is the
// client bundle in static/dist; a static page has none (no JavaScript).
func page(w http.ResponseWriter, title, entry string, render func(io.Writer) error) {
	buf := buffers.Get().(*bytes.Buffer)
	buf.Reset()
	defer buffers.Put(buf)
	buf.WriteString(headStart)
	buf.WriteString(htmlEscape(title))
	buf.WriteString(headEnd)
	if err := render(buf); err != nil {
		log.Printf("render %s: %v", title, err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	buf.WriteString(`</div>`)
	if entry != "" {
		buf.WriteString(`<script type="module" src="/static/dist/` + entry + `.js"></script>`)
	}
	buf.WriteString(`</body></html>`)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Write(buf.Bytes())
}

func (a *app) boardPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		notFound(w)
		return
	}
	project, columns, assignees, err := a.board(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	props := pages.BoardPageProps{Project: project, Columns: columns, Assignees: assignees, Live: true}
	page(w, "Board", "board", func(out io.Writer) error {
		return pages.RenderBoardPage(out, props, pages.BoardPageSlots{})
	})
}

func (a *app) renderBoard(w http.ResponseWriter, r *http.Request) {
	n := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil {
		n = v
	}
	n = max(0, min(n, len(a.fixture)))
	columns, assignees := groupColumns(a.fixture[:n])
	props := pages.BoardPageProps{Project: fixtureProject, Columns: columns, Assignees: assignees, Live: false}
	page(w, "Board", "board", func(out io.Writer) error {
		return pages.RenderBoardPage(out, props, pages.BoardPageSlots{})
	})
}

func (a *app) issuesPage(w http.ResponseWriter, r *http.Request) {
	f := parseFilters(r)
	projects, err := a.projects(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	rows, total, pageCount, err := a.issueList(r.Context(), f)
	if err != nil {
		serverError(w, err)
		return
	}
	props := pages.IssuesPageProps{Projects: projects, Filters: f.object(), Rows: rows, Total: float64(total), Pages: float64(pageCount)}
	page(w, "Issues", "issues", func(out io.Writer) error {
		return pages.RenderIssuesPage(out, props, pages.IssuesPageSlots{})
	})
}

func (a *app) issuePage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	issue, comments, attachments, err := a.issue(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	props := pages.IssuePageProps{Issue: issue, Comments: comments, Attachments: attachments}
	page(w, "Issue", "issue", func(out io.Writer) error {
		return pages.RenderIssuePage(out, props, pages.IssuePageSlots{})
	})
}

func (a *app) dashboardPage(w http.ResponseWriter, r *http.Request) {
	d, err := a.dashboard(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	props := pages.DashboardPageProps{Totals: d.totals, ByStatus: d.byStatus, ByPriority: d.byPriority, Projects: d.projects, Recent: d.recent}
	// A static root: plain HTML, no client entry (vela.manifest.json).
	page(w, "Dashboard", "", func(out io.Writer) error {
		return pages.RenderDashboardPageStatic(out, props, pages.DashboardPageSlots{})
	})
}

// ── JSON helpers ───────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		serverError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(status)
	w.Write(data)
}

func jsonError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func notFound(w http.ResponseWriter) { jsonError(w, http.StatusNotFound, "not found") }

func serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	jsonError(w, http.StatusInternalServerError, "internal error")
}

func htmlEscape(s string) string {
	var b bytes.Buffer
	for _, c := range s {
		switch c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
