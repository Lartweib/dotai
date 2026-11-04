// Package server serves the local web UI of dotai ui (decision 0043): an
// HTTP server bound to 127.0.0.1 that serves the frontend embedded in the
// binary and a JSON API. The API handlers call the use cases in
// internal/app directly, never the command line, and answer with the
// envelope of internal/envelope, as --json does. The package is a
// presentation layer and holds no logic of its own. Every request must
// name the server by its own host and come from its own origin; every API
// request but the session exchange must carry the session token
// (guard.go, session.go).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

// Deps holds the use cases the API calls, as cli.Env holds them for the
// command line, so that a test can fake each one. AppDeps returns the
// real ones.
type Deps struct {
	Version               func() app.VersionInfo
	Status                func(home string) (app.Status, error)
	PlanInstall           func(ctx context.Context, cacheDir, dir, home string, opts app.InstallOptions, progress app.Progress) (app.InstallPlan, error)
	PlanProject           func(ctx context.Context, cacheDir, dir, cwd, home string, opts app.ApplyOptions, progress app.Progress) (app.ProjectPlan, error)
	Backups               func(home string) (app.Backups, error)
	PreviewRestore        func(ctx context.Context, home, id string) (app.RestoreResult, error)
	PreviewProjectRestore func(ctx context.Context, cwd, home string) (app.ProjectRestoreResult, error)
	Doctor                func(ctx context.Context, home string, opts app.DoctorOptions) (app.DoctorResult, error)
	Validate              func(dir string, sel app.Selection) (app.ValidateResult, error)
	Harnesses             func() []app.HarnessInfo
	Install               func(ctx context.Context, cacheDir, dir, home string, opts app.InstallOptions, progress app.Progress, warn app.Warn, consent app.Consent) (app.InstallResult, error)
	Apply                 func(ctx context.Context, cacheDir, dir, cwd, home string, opts app.ApplyOptions, progress app.Progress, consent app.ApplyConsent) (app.ApplyResult, error)
	Restore               func(ctx context.Context, home, id string, opts app.RestoreOptions, warn app.Warn, overwrite func(app.RestoreResult) (bool, error)) (app.RestoreResult, error)
	RestoreProject        func(ctx context.Context, cwd, home string, opts app.ProjectRestoreOptions, overwrite func(app.ProjectRestoreResult) (bool, error)) (app.ProjectRestoreResult, error)
}

// AppDeps returns the use cases of internal/app, which fetch and run
// rulesync and list processes through p.
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
		Validate:              app.Validate,
		Harnesses:             app.Harnesses,
		Install:               p.Install,
		Apply:                 p.Apply,
		Restore:               p.Restore,
		RestoreProject:        app.RestoreProject,
	}
}

// Config is what a handler serves: the absolute home and rulesync cache
// directories the use cases receive, and the port the server listens on,
// which the Host and Origin checks compare with. Code is the one-time code
// of the address dotai ui opens, good for one exchange until CodeExpires;
// the exchange returns Session, the token every other API request must
// carry; CodeSpent, when it is not nil, is called once the code is
// traded. Context is the life of the server (context.Background when it is
// nil): an action is cancelled only when it ends, never by its request.
// OperationTimeout stops a plan or an action that runs longer
// (defaultOperationTimeout when it is 0), and Now is the clock (time.Now when it is nil).
type Config struct {
	Home, CacheDir   string
	Port             int
	Code             string
	CodeExpires      time.Time
	Session          string
	CodeSpent        func()
	OperationTimeout time.Duration
	Now              func() time.Time
	Context          context.Context
}

// defaultOperationTimeout bounds a plan or an action, which may download
// rulesync first.
const defaultOperationTimeout = 5 * time.Minute

// Error codes of the API besides those of the command line
// (internal/envelope): a request the guard refuses, an unknown path, a
// method the path does not take, a plan or an action asked for while
// another runs, and an action whose plan is no longer the one its digest
// names (digest.go).
const (
	CodeForbidden        = "forbidden"
	CodeUnauthorized     = "unauthorized"
	CodeNotFound         = "not-found"
	CodeMethodNotAllowed = "method-not-allowed"
	CodeBusy             = "busy"
	CodePlanChanged      = "plan-changed"
)

// serverCodes lists the codes above.
var serverCodes = []string{CodeForbidden, CodeUnauthorized, CodeNotFound, CodeMethodNotAllowed, CodeBusy, CodePlanChanged}

// command names the envelopes of the session exchange and of the requests
// that reach no endpoint.
const command = "ui"

// apiError reports a failure of the API itself, with its own code and
// HTTP status, such as a refused session exchange or a busy plan, and
// the data that explains it, nil when there is none.
type apiError struct {
	code   string
	status int
	msg    string
	data   any
}

func (e *apiError) Error() string { return e.msg }

// New returns the handler of the web UI for cfg. Its endpoints call deps,
// and every other path serves the frontend in web.
func New(cfg Config, deps Deps, web fs.FS) http.Handler {
	return newUI(cfg, deps, web)
}

// ui is the handler of New, with the API it serves, which Run waits for.
type ui struct {
	http.Handler
	api *handler
}

// wait waits up to d for the actions in flight, and their rollback, to
// end, and reports whether they did.
func (u *ui) wait(d time.Duration) bool {
	done := make(chan struct{})
	go func() { u.api.actions.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func newUI(cfg Config, deps Deps, web fs.FS) *ui {
	if cfg.Context == nil {
		cfg.Context = context.Background()
	}
	if cfg.OperationTimeout == 0 {
		cfg.OperationTimeout = defaultOperationTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &handler{cfg: cfg, deps: deps, ops: make(chan struct{}, 1),
		session: &session{code: cfg.Code, expires: cfg.CodeExpires, token: cfg.Session, now: cfg.Now, onSpent: cfg.CodeSpent}}
	api := map[string]endpoint{
		"/api/session":                 {command, http.MethodPost, s.exchange},
		"/api/version":                 {"version", http.MethodGet, s.version},
		"/api/status":                  {"status", http.MethodGet, s.status},
		"/api/install/plan":            {"install", http.MethodPost, s.planInstall},
		"/api/apply/plan":              {"apply", http.MethodPost, s.planProject},
		"/api/backups":                 {"backups", http.MethodGet, s.backups},
		"/api/restore/preview":         {"restore", http.MethodGet, s.previewRestore},
		"/api/restore/project/preview": {"restore", http.MethodGet, s.previewProjectRestore},
		"/api/doctor":                  {"doctor", http.MethodGet, s.doctor},
		"/api/pack":                    {"validate", http.MethodGet, s.pack},
		"/api/harnesses":               {command, http.MethodGet, s.harnesses},
		"/api/install":                 {"install", http.MethodPost, s.install},
		"/api/apply":                   {"apply", http.MethodPost, s.apply},
		"/api/restore":                 {"restore", http.MethodPost, s.restore},
		"/api/restore/project":         {"restore", http.MethodPost, s.restoreProject},
		"/api/doctor/fix":              {"doctor", http.MethodPost, s.fix},
	}
	files := http.FileServerFS(web)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		e, ok := api[r.URL.Path]
		if !ok {
			writeEnvelope(w, http.StatusNotFound, envelope.Failure(command, CodeNotFound, "no endpoint "+r.URL.Path))
			return
		}
		if r.Method != e.method {
			w.Header().Set("Allow", e.method)
			writeEnvelope(w, http.StatusMethodNotAllowed, envelope.Failure(e.command, CodeMethodNotAllowed, r.Method+" is not allowed on "+r.URL.Path+": use "+e.method))
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
	return &ui{Handler: guard(cfg, mux), api: s}
}

// handler serves the API. ops holds the one plan or action that may run
// at a time.
type handler struct {
	cfg     Config
	deps    Deps
	session *session
	ops     chan struct{}
	actions sync.WaitGroup
}

// endpoint describes one path of the API: the command its envelope names,
// as the command line names it, the one method it takes, and the function
// it runs, which returns the envelope's data.
type endpoint struct {
	command string
	method  string
	run     func(*http.Request) (any, error)
}

// exchange trades the one-time code in the body for the session token.
func (s *handler) exchange(r *http.Request) (any, error) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	token, err := s.session.exchange(req.Code)
	if err != nil {
		return nil, err
	}
	return map[string]string{"token": token}, nil
}

func (s *handler) version(*http.Request) (any, error) { return s.deps.Version(), nil }

// harnesses lists the harnesses dotai supports, with their names, so that
// the page names them as dotai does.
func (s *handler) harnesses(*http.Request) (any, error) {
	return map[string]any{"harnesses": s.deps.Harnesses()}, nil
}

func (s *handler) status(*http.Request) (any, error) { return s.deps.Status(s.cfg.Home) }

func (s *handler) backups(*http.Request) (any, error) { return s.deps.Backups(s.cfg.Home) }

// installRequest holds the body of a plan of an install: the absolute
// path of the pack and the options of install --dry-run. Harnesses is nil
// for the saved selection.
type installRequest struct {
	Pack      string   `json:"pack"`
	Variant   string   `json:"variant"`
	Mode      string   `json:"mode"`
	Harnesses []string `json:"harnesses"`
}

// planInstall runs install --dry-run as the body asks.
func (s *handler) planInstall(r *http.Request) (any, error) {
	var req installRequest
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	pack, err := absParam(req.Pack, "pack", true)
	if err != nil {
		return nil, err
	}
	opts := req.options()
	return s.plan(r, "install plan", func(ctx context.Context) (any, error) {
		p, err := s.deps.PlanInstall(ctx, s.cfg.CacheDir, pack, s.cfg.Home, opts, nil)
		if err != nil {
			return nil, err
		}
		if len(p.Diagnostics) > 0 {
			return nil, envelope.PackInvalid(p.Diagnostics, p)
		}
		return installView(p)
	})
}

// applyRequest holds the body of a plan of a project apply: the project
// directory, which the command would run in, the pack, by default the one
// the project or the install state names, and the options of apply
// --dry-run. Both paths are absolute.
type applyRequest struct {
	Project  string   `json:"project"`
	Pack     string   `json:"pack"`
	Profiles []string `json:"profiles"`
	Variant  string   `json:"variant"`
}

// planProject runs apply --dry-run as the body asks.
func (s *handler) planProject(r *http.Request) (any, error) {
	var req applyRequest
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	project, err := absParam(req.Project, "project", true)
	if err != nil {
		return nil, err
	}
	pack, err := absParam(req.Pack, "pack", false)
	if err != nil {
		return nil, err
	}
	opts := req.options()
	return s.plan(r, "apply plan", func(ctx context.Context) (any, error) {
		p, err := s.deps.PlanProject(ctx, s.cfg.CacheDir, pack, project, s.cfg.Home, opts, nil)
		if err != nil {
			return nil, err
		}
		if len(p.Diagnostics) > 0 {
			return nil, envelope.PackInvalid(p.Diagnostics, p)
		}
		return projectView(p)
	})
}

// plan runs a plan, what, which fetches rulesync into the cache, stages the pack
// into a temporary directory and may run git, as one operation.
func (s *handler) plan(r *http.Request, what string, run func(context.Context) (any, error)) (any, error) {
	return s.operate(r.Context(), what, run)
}

// act runs an action, what, which writes: on a context of its own that its
// request does not cancel, so that a closed tab or a reload does not stop
// it halfway, and that only the end of the server cancels, after which the
// use case rolls back what it wrote. Run waits for it.
func (s *handler) act(r *http.Request, what string, run func(context.Context) (any, error)) (any, error) {
	s.actions.Add(1)
	defer s.actions.Done()
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	stop := context.AfterFunc(s.cfg.Context, cancel)
	defer stop()
	return s.operate(ctx, what, run)
}

// operate runs a plan or an action, what. Only one runs at a time: a
// second one is refused as busy, not queued. One that runs longer than
// the plan timeout is cancelled, and an action that is cancelled rolls
// back what it wrote, as on the command line.
func (s *handler) operate(parent context.Context, what string, run func(context.Context) (any, error)) (any, error) {
	select {
	case s.ops <- struct{}{}:
		defer func() { <-s.ops }()
	default:
		return nil, &apiError{code: CodeBusy, status: http.StatusTooManyRequests, msg: "another plan or action is running: try again once it ends"}
	}
	ctx, cancel := context.WithTimeout(parent, s.cfg.OperationTimeout)
	defer cancel()
	data, err := run(ctx)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("the %s took longer than %s and was stopped: %w", what, s.cfg.OperationTimeout, err)
	}
	return data, err
}

// previewRestore previews the restore of the query parameter id, by
// default the last install.
func (s *handler) previewRestore(r *http.Request) (any, error) {
	res, err := s.deps.PreviewRestore(r.Context(), s.cfg.Home, r.URL.Query().Get("id"))
	if err != nil {
		return nil, err
	}
	return restorePreview(res)
}

// previewProjectRestore previews restore --project in the project at the
// query parameter project.
func (s *handler) previewProjectRestore(r *http.Request) (any, error) {
	project, err := absParam(r.URL.Query().Get("project"), "project", true)
	if err != nil {
		return nil, err
	}
	res, err := s.deps.PreviewProjectRestore(r.Context(), project, s.cfg.Home)
	if err != nil {
		return nil, err
	}
	return projectRestorePreview(res)
}

// doctor runs dotai doctor without --fix. Problems make an invalid
// outcome with the result, as on the command line. Either way the result
// carries its digest, which doctor --fix takes back.
func (s *handler) doctor(r *http.Request) (any, error) {
	res, err := s.deps.Doctor(r.Context(), s.cfg.Home, app.DoctorOptions{})
	if err != nil {
		return nil, err
	}
	data, err := doctorFindings(res)
	if err != nil {
		return nil, err
	}
	if err := envelope.DoctorProblems(res); err != nil {
		if ie, ok := errors.AsType[*envelope.InvalidError](err); ok {
			ie.Data = data
		}
		return nil, err
	}
	return data, nil
}

// absParam checks v, the parameter name, a path that must be absolute:
// the server has no current directory of the user's to resolve a relative
// one against. An empty v is a usage error when required.
func absParam(v, name string, required bool) (string, error) {
	switch {
	case v == "" && required:
		return "", envelope.Usage("the parameter " + name + " is required")
	case v == "":
		return "", nil
	case !filepath.IsAbs(v):
		return "", envelope.Usage(fmt.Sprintf("the parameter %s must be an absolute path, not %q", name, v))
	}
	return filepath.Clean(v), nil
}

// maxBody bounds the JSON body of a request.
const maxBody = 64 << 10

// decodeJSON decodes the body of r into v. The body must be one JSON
// object, sent as application/json, with no field that v lacks.
func decodeJSON(r *http.Request, v any) error {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return envelope.Usage("the body must be JSON, with Content-Type application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return envelope.Usage("the body is not the JSON this endpoint takes: " + err.Error())
	}
	if dec.More() {
		return envelope.Usage("the body holds more than one JSON value")
	}
	return nil
}
