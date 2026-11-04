// Package server is the local web UI of dotai ui (decision 0043): an HTTP
// server bound to 127.0.0.1 that serves the frontend embedded in the
// binary and a JSON API whose handlers call the use cases in internal/app
// directly, never the command line, and answer with the envelope of
// internal/envelope, as --json does. It is a presentation layer only:
// it holds no logic of its own. Every request must name the server by its
// own host and, when it carries an Origin, come from the server's own
// origin; every request but the first, which trades the token of the URL
// for a cookie, must carry the token of the run (guard.go).
package server

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

// Deps are the use cases the API calls, as cli.Env holds them for the
// command line, so that a test can fake each one. AppDeps gives the real
// ones.
type Deps struct {
	Version               func() app.VersionInfo
	Status                func(home string) (app.Status, error)
	PlanInstall           func(ctx context.Context, cacheDir, dir, home string, opts app.InstallOptions, progress app.Progress) (app.InstallPlan, error)
	PlanProject           func(ctx context.Context, cacheDir, dir, cwd, home string, opts app.ApplyOptions, progress app.Progress) (app.ProjectPlan, error)
	Backups               func(home string) (app.Backups, error)
	PreviewRestore        func(ctx context.Context, home, id string) (app.RestoreResult, error)
	PreviewProjectRestore func(ctx context.Context, cwd, home string) (app.ProjectRestoreResult, error)
	Doctor                func(ctx context.Context, home string, opts app.DoctorOptions) (app.DoctorResult, error)
}

// AppDeps are the use cases of internal/app, fetching and running rulesync
// and listing processes through p.
func AppDeps(p app.Ports) Deps {
	return Deps{
		Version:               app.Version,
		Status:                app.ReadStatus,
		PlanInstall:           p.PlanInstall,
		PlanProject:           p.PlanProject,
		Backups:               app.ListBackups,
		PreviewRestore:        app.PreviewRestore,
		PreviewProjectRestore: app.PreviewProjectRestore,
		Doctor:                app.Doctor,
	}
}

// Config is what a handler serves: the absolute home directory and
// rulesync cache directory the use cases are given, the token of the run,
// and the port the server listens on, which the Host and Origin checks
// compare with.
type Config struct {
	Home, CacheDir string
	Token          string
	Port           int
}

// Error codes of the API besides those of the command line
// (internal/envelope): a request refused by the guard, an unknown path,
// and a method the path does not take.
const (
	CodeForbidden        = "forbidden"
	CodeUnauthorized     = "unauthorized"
	CodeNotFound         = "not-found"
	CodeMethodNotAllowed = "method-not-allowed"
)

// command names the envelopes of the requests that reach no endpoint.
const command = "ui"

// New is the handler of the web UI for cfg, calling deps, with the
// frontend in web.
func New(cfg Config, deps Deps, web fs.FS) http.Handler {
	s := &handler{cfg: cfg, deps: deps}
	api := map[string]endpoint{
		"/api/version":                 {"version", s.version},
		"/api/status":                  {"status", s.status},
		"/api/install/plan":            {"install", s.planInstall},
		"/api/apply/plan":              {"apply", s.planProject},
		"/api/backups":                 {"backups", s.backups},
		"/api/restore/preview":         {"restore", s.previewRestore},
		"/api/restore/project/preview": {"restore", s.previewProjectRestore},
		"/api/doctor":                  {"doctor", s.doctor},
	}
	files := http.FileServerFS(web)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		e, ok := api[r.URL.Path]
		if !ok {
			writeEnvelope(w, http.StatusNotFound, envelope.Failure(command, CodeNotFound, "no endpoint "+r.URL.Path))
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeEnvelope(w, http.StatusMethodNotAllowed, envelope.Failure(e.command, CodeMethodNotAllowed, r.Method+" is not allowed on "+r.URL.Path+": it is read-only, use GET"))
			return
		}
		data, err := e.run(r)
		if err != nil {
			writeFailure(w, e.command, err)
			return
		}
		writeEnvelope(w, http.StatusOK, envelope.Success(e.command, data))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		files.ServeHTTP(w, r)
	})
	return guard(cfg, mux)
}

// handler serves the API.
type handler struct {
	cfg  Config
	deps Deps
}

// endpoint is one path of the API: the command its envelope names, as the
// command line names it, and what it runs, which gives the envelope's data.
type endpoint struct {
	command string
	run     func(*http.Request) (any, error)
}

func (s *handler) version(*http.Request) (any, error) { return s.deps.Version(), nil }

func (s *handler) status(*http.Request) (any, error) { return s.deps.Status(s.cfg.Home) }

func (s *handler) backups(*http.Request) (any, error) { return s.deps.Backups(s.cfg.Home) }

// planInstall is install --dry-run of the pack at the query parameter
// pack, with variant, mode and each harness.
func (s *handler) planInstall(r *http.Request) (any, error) {
	q := r.URL.Query()
	pack, err := absParam(q.Get("pack"), "pack", true)
	if err != nil {
		return nil, err
	}
	opts := app.InstallOptions{Variant: q.Get("variant"), Mode: q.Get("mode")}
	if h, ok := q["harness"]; ok {
		opts.Harnesses = splitList(h)
	}
	p, err := s.deps.PlanInstall(r.Context(), s.cfg.CacheDir, pack, s.cfg.Home, opts, nil)
	if err != nil {
		return nil, err
	}
	if len(p.Diagnostics) > 0 {
		return nil, envelope.PackInvalid(p.Diagnostics, p)
	}
	return p, nil
}

// planProject is apply --dry-run in the project at the query parameter
// project, of the pack at pack, by default the one the project or the
// install state names, with variant and each profile.
func (s *handler) planProject(r *http.Request) (any, error) {
	q := r.URL.Query()
	project, err := absParam(q.Get("project"), "project", true)
	if err != nil {
		return nil, err
	}
	pack, err := absParam(q.Get("pack"), "pack", false)
	if err != nil {
		return nil, err
	}
	opts := app.ApplyOptions{Profiles: splitList(q["profile"]), Variant: q.Get("variant")}
	p, err := s.deps.PlanProject(r.Context(), s.cfg.CacheDir, pack, project, s.cfg.Home, opts, nil)
	if err != nil {
		return nil, err
	}
	if len(p.Diagnostics) > 0 {
		return nil, envelope.PackInvalid(p.Diagnostics, p)
	}
	return p, nil
}

// previewRestore is what restore of the query parameter id, by default the
// last install, would do.
func (s *handler) previewRestore(r *http.Request) (any, error) {
	return s.deps.PreviewRestore(r.Context(), s.cfg.Home, r.URL.Query().Get("id"))
}

// previewProjectRestore is what restore --project would do in the project
// at the query parameter project.
func (s *handler) previewProjectRestore(r *http.Request) (any, error) {
	project, err := absParam(r.URL.Query().Get("project"), "project", true)
	if err != nil {
		return nil, err
	}
	return s.deps.PreviewProjectRestore(r.Context(), project, s.cfg.Home)
}

// doctor is dotai doctor without --fix: problems are an invalid outcome
// with the result, as with the command line.
func (s *handler) doctor(r *http.Request) (any, error) {
	res, err := s.deps.Doctor(r.Context(), s.cfg.Home, app.DoctorOptions{})
	if err != nil {
		return nil, err
	}
	if err := envelope.DoctorProblems(res); err != nil {
		return nil, err
	}
	return res, nil
}

// absParam is the query parameter name, the path v, which must be
// absolute: the server has no current directory of the user's to resolve
// a relative one against. "" is a usage error when required.
func absParam(v, name string, required bool) (string, error) {
	switch {
	case v == "" && required:
		return "", envelope.Usage("the query parameter " + name + " is required")
	case v == "":
		return "", nil
	case !filepath.IsAbs(v):
		return "", envelope.Usage(fmt.Sprintf("the query parameter %s must be an absolute path, not %q", name, v))
	}
	return filepath.Clean(v), nil
}

// splitList is the values of a repeated query parameter, each of which
// may also be a comma-separated list, as the command line's flags take
// them; never nil.
func splitList(values []string) []string {
	out := []string{}
	for _, v := range values {
		for item := range strings.SplitSeq(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}
