package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	vela "github.com/elauven/vela/ssr_runtime"
)

// TestBoundaryErrorsAreLogged: an error a {#boundary} catches while the
// page renders goes to Config.Logger with the path, the route and the
// position of the block, and the page renders (task 091).
func TestBoundaryErrorsAreLogged(t *testing.T) {
	var logs bytes.Buffer
	routes := testRoutes(nil)
	routes.Root.Render = func(ctx context.Context, w io.Writer, props RootProps) error {
		w = vela.WithContext(w, ctx)
		mark := vela.BeginBoundary(w)
		if _, err := vela.CatchBoundary(w, mark, errors.New("chart service down"), nil, "src/routes/Page.vela:12:3"); err != nil {
			return err
		}
		return fakeRoot(ctx, w, props)
	}
	h := newTestHandler(t, Config{Routes: routes, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	p := serve(t, h, "GET", "/users/2")
	if p.status != 200 {
		t.Fatalf("status %d\n%s", p.status, p.body)
	}
	for _, want := range []string{`msg="vela app: {#boundary} caught a render error"`, "path=/users/2", `route="/users/[id=int]"`, "source=src/routes/Page.vela:12:3", `err="chart service down"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log misses %s:\n%s", want, logs.String())
		}
	}
}
