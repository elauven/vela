// Package crash shows the error page of an internal error.
package crash

import (
	"context"
	"errors"

	"github.com/elauven/vela/app"
)

// Page fails: the server logs the error and answers 500 with Error.vela.
func Page(ctx context.Context, r *app.Request) (any, error) {
	return nil, errors.New("the example fails on purpose")
}
