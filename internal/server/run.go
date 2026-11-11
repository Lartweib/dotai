package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/lartweib/dotai/internal/app"
)

// Options holds the choices of a run of dotai ui: the absolute home and
// rulesync cache directories the use cases receive, and the port, 0 for a
// free one. Run calls Ready once the server listens, with the address to
// open, which carries the one-time code; Ready may be nil. Open, when it is
// not nil, is then given the path of a page, readable by the user alone,
// that sends the browser to that address, so that the program that opens
// the browser never has the code on its command line; the page is deleted
// once the code is traded, or when Run returns.
type Options struct {
	Home, CacheDir string
	Port           int
	Ready          func(url string) error
	Open           func(page string) error
}

// shutdownWait is how long a stop waits for the requests in flight, whose
// context is cancelled, before it closes their connections.
const shutdownWait = 5 * time.Second

// Run serves the web UI on 127.0.0.1 until ctx is done, such as on Ctrl+C,
// then shuts the server down: it stops accepting, cancels the requests in
// flight and waits for them. Each run makes a new one-time code and a new
// session token. Run fails when it cannot listen on the port, when Ready
// fails, or when the server stops on its own.
func Run(ctx context.Context, opts Options, deps Deps) error {
	if opts.Port < 0 || opts.Port > 65535 {
		return fmt.Errorf("the port %d is not between 0 and 65535", opts.Port)
	}
	code, err := NewToken()
	if err != nil {
		return err
	}
	token, err := NewToken()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.Port)))
	if err != nil {
		return fmt.Errorf("dotai ui cannot listen on 127.0.0.1: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cfg := Config{Home: opts.Home, CacheDir: opts.CacheDir, Port: port, Code: code, CodeExpires: time.Now().Add(codeTTL), Session: token}
	var page string
	if opts.Open != nil {
		var remove func()
		if page, remove, err = writeLauncher(URL(port, code)); err != nil {
			ln.Close()
			return err
		}
		defer remove()
		cfg.CodeSpent = remove
	}
	cfg.Context = ctx
	h := newUI(cfg, deps, Web())
	srv := newHTTPServer(ctx, h)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	if opts.Ready != nil {
		if err := opts.Ready(URL(port, code)); err != nil {
			return errors.Join(err, shutdown(srv, served, h))
		}
	}
	if opts.Open != nil {
		if err := opts.Open(page); err != nil {
			return errors.Join(err, shutdown(srv, served, h))
		}
	}
	select {
	case err := <-served:
		return fmt.Errorf("dotai ui stopped: %w", err)
	case <-ctx.Done():
		return shutdown(srv, served, h)
	}
}

// newHTTPServer returns the server of h, whose requests get a context
// that ctx cancels, with limits on slow and idle connections and on the
// size of headers.
func newHTTPServer(ctx context.Context, h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
}

// URL returns the address of the server on port, with the one-time code
// in its fragment.
func URL(port int, code string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/#%s=%s", port, CodeParam, code)
}

// actionWait is how long a stop waits for an action in flight, whose
// context the stop cancels, to roll back what it wrote.
const actionWait = app.RollbackTimeout + 15*time.Second

// shutdown stops srv, whose Serve sends its outcome to served, once the
// actions of h in flight have ended, up to actionWait.
func shutdown(srv *http.Server, served <-chan error, h *ui) error {
	h.wait(actionWait)
	ctx, cancel := context.WithTimeout(context.Background(), shutdownWait)
	defer cancel()
	err := srv.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = srv.Close()
	}
	if serr := <-served; !errors.Is(serr, http.ErrServerClosed) {
		err = errors.Join(err, serr)
	}
	return err
}
