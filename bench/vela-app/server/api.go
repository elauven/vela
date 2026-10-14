package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	vela "github.com/elauven/vela/ssr_runtime"
)

const maxUpload = 100 << 20

func (a *app) apiIssues(w http.ResponseWriter, r *http.Request) {
	rows, total, pages, err := a.issueList(r.Context(), parseFilters(r))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vela.NewObject().Set("rows", rows).Set("total", total).Set("pages", pages))
}

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(into); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return false
	}
	return true
}

func (a *app) apiCreateIssue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID int     `json:"projectId"`
		Title     string  `json:"title"`
		Status    *string `json:"status"`
		Priority  *int    `json:"priority"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	status, priority := "backlog", 0
	if in.Status != nil {
		status = *in.Status
	}
	if in.Priority != nil {
		priority = *in.Priority
	}
	if in.Title == "" || statusTitles[status] == "" || priority < 0 || priority > 3 {
		jsonError(w, http.StatusBadRequest, "invalid issue")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var number int64
	var key string
	err = tx.QueryRow(ctx, `UPDATE projects SET next_number = next_number + 1 WHERE id = $1 RETURNING next_number - 1, key`, in.ProjectID).Scan(&number, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		jsonError(w, http.StatusBadRequest, "unknown project")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO issues (project_id, number, title, status, priority, position, created_at, updated_at)
	  VALUES ($1, $2, $3, $4, $5, (SELECT coalesce(max(position), 0) + 1024 FROM issues WHERE project_id = $1 AND status = $4), now(), now())
	  RETURNING id`, in.ProjectID, number, in.Title, status, priority).Scan(&id)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, vela.NewObject().Set("id", id).Set("key", fmt.Sprintf("%s-%d", key, number)))
}

func (a *app) apiPatchIssue(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	var in struct {
		Title    *string  `json:"title"`
		Status   *string  `json:"status"`
		Priority *int     `json:"priority"`
		Position *float64 `json:"position"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" {
			jsonError(w, http.StatusBadRequest, "empty title")
			return
		}
		in.Title = &t
	}
	if (in.Status != nil && statusTitles[*in.Status] == "") || (in.Priority != nil && (*in.Priority < 0 || *in.Priority > 3)) {
		jsonError(w, http.StatusBadRequest, "invalid field")
		return
	}
	// A status change without a position appends to the end of the column.
	var title, status string
	var priority int16
	var position float64
	err = a.db.QueryRow(r.Context(), `
UPDATE issues i SET
  title    = coalesce($2, i.title),
  status   = coalesce($3, i.status),
  priority = coalesce($4, i.priority),
  position = CASE WHEN $5::double precision IS NOT NULL THEN $5
                  WHEN $3::text IS NOT NULL AND $3 <> i.status THEN
                    (SELECT coalesce(max(o.position), 0) + 1024 FROM issues o WHERE o.project_id = i.project_id AND o.status = $3)
                  ELSE i.position END,
  updated_at = now()
WHERE i.id = $1
RETURNING i.title, i.status, i.priority, i.position`, id, in.Title, in.Status, in.Priority, in.Position).Scan(&title, &status, &priority, &position)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vela.NewObject().Set("id", id).Set("title", title).Set("status", status).Set("priority", int64(priority)).Set("position", position))
}

func (a *app) apiComment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		jsonError(w, http.StatusBadRequest, "empty comment")
		return
	}
	var cid int64
	var name, color string
	var at time.Time
	err = a.db.QueryRow(r.Context(), `
WITH c AS (INSERT INTO comments (issue_id, author_id, body, created_at)
           SELECT id, 1, $2, now() FROM issues WHERE id = $1 RETURNING id, author_id, created_at)
SELECT c.id, u.name, u.color, c.created_at FROM c JOIN users u ON u.id = c.author_id`, id, body).Scan(&cid, &name, &color, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, commentObject(cid, name, color, body, at))
}

// apiUpload streams the "file" part to STORAGE_DIR, hashing while it writes.
func (a *app) apiUpload(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM issues WHERE id = $1)`, id).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		notFound(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	reader, err := r.MultipartReader()
	if err != nil {
		jsonError(w, http.StatusBadRequest, "multipart body expected")
		return
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			jsonError(w, http.StatusBadRequest, `field "file" missing`)
			return
		}
		if err != nil {
			jsonError(w, http.StatusBadRequest, "invalid multipart body")
			return
		}
		if part.FormName() != "file" {
			part.Close()
			continue
		}
		a.store(w, r, id, part.FileName(), part.Header.Get("Content-Type"), part)
		return
	}
}

func (a *app) store(w http.ResponseWriter, r *http.Request, issueID int64, filename, contentType string, src io.Reader) {
	filename = filepath.Base(strings.ReplaceAll(filename, `\`, "/"))
	if filename == "." || filename == "/" || filename == "" {
		filename = "file"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	random := make([]byte, 16)
	rand.Read(random)
	key := hex.EncodeToString(random)
	path := filepath.Join(a.storage, key)
	file, err := os.Create(path)
	if err != nil {
		serverError(w, err)
		return
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), src)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		jsonError(w, http.StatusBadRequest, "upload failed")
		return
	}
	var aid int64
	var at time.Time
	err = a.db.QueryRow(r.Context(), `INSERT INTO attachments (issue_id, filename, content_type, size, sha256, storage_key, created_at)
	  VALUES ($1, $2, $3, $4, $5, $6, now()) RETURNING id, created_at`, issueID, filename, contentType, size, hex.EncodeToString(hash.Sum(nil)), key).Scan(&aid, &at)
	if err != nil {
		os.Remove(path)
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, attachmentObject(aid, filename, size, at))
}

// file streams an attachment; http.ServeContent handles Range and
// conditional requests.
func (a *app) file(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	var filename, contentType, sum, key string
	var at time.Time
	err = a.db.QueryRow(r.Context(), `SELECT filename, content_type, sha256, storage_key, created_at FROM attachments WHERE id = $1`, id).
		Scan(&filename, &contentType, &sum, &key, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	f, err := os.Open(filepath.Join(a.storage, key))
	if err != nil {
		notFound(w)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("ETag", `"`+sum+`"`)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	http.ServeContent(w, r, filename, at, f)
}
