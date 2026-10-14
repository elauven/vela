-- Schema of the benchmark issue tracker (task 058). Both applications use
-- it unchanged; the seed (bench/seed) fills it deterministically.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

DROP TABLE IF EXISTS attachments, comments, issue_labels, labels, issues, projects, users CASCADE;

CREATE TABLE users (
  id          integer PRIMARY KEY,
  name        text    NOT NULL,
  email       text    NOT NULL UNIQUE,
  color       text    NOT NULL           -- avatar background, #rrggbb
);

CREATE TABLE projects (
  id          integer PRIMARY KEY,
  key         text    NOT NULL UNIQUE,   -- short code, e.g. "WEB"
  name        text    NOT NULL,
  description text    NOT NULL DEFAULT '',
  next_number integer NOT NULL DEFAULT 1 -- next issue number, taken with UPDATE … RETURNING
);

CREATE TABLE issues (
  id          bigserial PRIMARY KEY,
  project_id  integer NOT NULL REFERENCES projects(id),
  number      integer NOT NULL,          -- per project: WEB-42
  title       text    NOT NULL,
  description text    NOT NULL DEFAULT '',
  status      text    NOT NULL CHECK (status IN ('backlog', 'todo', 'in_progress', 'done')),
  priority    smallint NOT NULL CHECK (priority BETWEEN 0 AND 3),  -- 0 none … 3 urgent
  assignee_id integer REFERENCES users(id),
  position    double precision NOT NULL, -- order inside a board column
  due_date    date,
  created_at  timestamptz NOT NULL,
  updated_at  timestamptz NOT NULL,
  UNIQUE (project_id, number)
);
CREATE INDEX issues_board   ON issues (project_id, status, position);
CREATE INDEX issues_updated ON issues (project_id, updated_at DESC);
CREATE INDEX issues_title   ON issues USING gin (title gin_trgm_ops);
CREATE INDEX issues_recent  ON issues (updated_at DESC);

CREATE TABLE labels (
  id          integer PRIMARY KEY,
  project_id  integer NOT NULL REFERENCES projects(id),
  name        text    NOT NULL,
  color       text    NOT NULL
);

CREATE TABLE issue_labels (
  issue_id    bigint  NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  label_id    integer NOT NULL REFERENCES labels(id),
  PRIMARY KEY (issue_id, label_id)
);
CREATE INDEX issue_labels_label ON issue_labels (label_id);

CREATE TABLE comments (
  id          bigserial PRIMARY KEY,
  issue_id    bigint  NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  author_id   integer NOT NULL REFERENCES users(id),
  body        text    NOT NULL,
  created_at  timestamptz NOT NULL
);
CREATE INDEX comments_issue ON comments (issue_id, created_at);

CREATE TABLE attachments (
  id           bigserial PRIMARY KEY,
  issue_id     bigint  NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  filename     text    NOT NULL,
  content_type text    NOT NULL,
  size         bigint  NOT NULL,
  sha256       text    NOT NULL,
  storage_key  text    NOT NULL UNIQUE,  -- file name under the storage directory
  created_at   timestamptz NOT NULL
);
CREATE INDEX attachments_issue ON attachments (issue_id, created_at);
