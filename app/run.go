package app

import (
	"context"
	"encoding/json"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/elauven/vela/app/internal/value"
)

// chainRun runs the loaders of one chain (docs/APP.md L-4, L-5). Each
// level is a future: it starts once, and Parent waits for it. An error at
// a level cancels the levels below it, whose output the error page
// replaces; the levels above it keep running, since they still render.
type chainRun struct {
	levels []*levelRun
}

type levelRun struct {
	level   *Level
	req     *Request
	ctx     context.Context
	cancel  context.CancelFunc
	once    sync.Once
	started atomic.Bool
	done    chan struct{}
	result  any             // the loader's result
	view    any             // its JSON view (L-2)
	data    json.RawMessage // its JSON
	err     error
}

func newChainRun(ctx context.Context, chain []Level, base Request) *chainRun {
	run := &chainRun{}
	for i := range chain {
		levelCtx, cancel := context.WithCancel(ctx)
		req := base
		req.run, req.level = run, i
		req.head, req.header = Head{}, nil
		run.levels = append(run.levels, &levelRun{level: &chain[i], req: &req, ctx: levelCtx, cancel: cancel, done: make(chan struct{})})
	}
	return run
}

// start runs a level's loader once, in its own goroutine.
func (c *chainRun) start(i int) {
	l := c.levels[i]
	l.once.Do(func() {
		l.started.Store(true)
		if l.level.Loader == nil {
			close(l.done)
			return
		}
		go func() {
			defer close(l.done)
			defer func() {
				if v := recover(); v != nil {
					l.err = &PanicError{Value: v, Stack: debug.Stack()}
				}
				if l.err != nil {
					c.fail(i)
				}
			}()
			result, err := l.level.Loader(l.ctx, l.req)
			if err != nil {
				l.err = err
				return
			}
			l.result = result
			l.view, l.data, l.err = value.FromGo(result)
		}()
	})
}

// preset gives a level its error without running its loader, as if the
// loader had failed, and cancels the levels below it.
func (c *chainRun) preset(i int, err error) {
	l := c.levels[i]
	l.once.Do(func() {
		l.started.Store(true)
		l.err = err
		close(l.done)
	})
	c.fail(i)
}

// wait starts a level if needed and returns its result.
func (c *chainRun) wait(i int) (any, error) {
	c.start(i)
	l := c.levels[i]
	<-l.done
	return l.result, l.err
}

// fail cancels the levels below a failed one.
func (c *chainRun) fail(i int) {
	for _, l := range c.levels[i+1:] {
		l.cancel()
	}
}

// runAll starts every level and waits for them in order. It returns the
// index of the first level that failed, or -1. Levels below a failed one
// are not waited for.
func (c *chainRun) runAll() int {
	for i := range c.levels {
		c.start(i)
	}
	for i := range c.levels {
		if _, err := c.wait(i); err != nil {
			return i
		}
	}
	return -1
}

// runFrom runs a data navigation that sends the levels from start (N-4):
// it starts those levels and the levels above start whose loaders may set
// the head (Level.Head), and waits for them. A level above start that no
// one needs never runs; Parent starts one on demand. It returns the first
// level, in level order, of the levels that ran and failed, or -1.
func (c *chainRun) runFrom(start int) int {
	for i, l := range c.levels {
		if i >= start || l.level.Head && l.level.Loader != nil {
			c.start(i)
		}
	}
	for i, l := range c.levels {
		if l.started.Load() {
			c.wait(i)
		}
	}
	// Every level that started is done; a Parent call started any level
	// above start that ran without being listed above, and waited for it.
	for i, l := range c.levels {
		if l.started.Load() {
			if _, err := c.wait(i); err != nil {
				return i
			}
		}
	}
	return -1
}

// ran reports whether a level's loader ran.
func (c *chainRun) ran(i int) bool {
	return c.levels[i].started.Load()
}

// close cancels the levels and waits for the loaders that started, so no
// loader outlives its request (it may use r.HTTP, which net/http reuses
// once ServeHTTP returns).
func (c *chainRun) close() {
	for _, l := range c.levels {
		l.cancel()
	}
	for _, l := range c.levels {
		if l.started.Load() {
			<-l.done
		}
	}
}
