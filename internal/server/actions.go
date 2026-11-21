package server

import (
	"context"
	"net/http"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

// The actions of the API, which write: install, apply, restore, restore
// --project and doctor --fix. Each one takes the digest of the plan or
// preview its user saw, and carries out only that plan (digest.go); yes
// is the consent the command line takes as --yes. An action runs as one
// operation, as a plan does, and never skips the running-harness check.

// confirmation is what every action takes besides its own choices: the
// digest of the plan the user saw and the consent to what it overwrites.
type confirmation struct {
	Digest string `json:"digest"`
	Yes    bool   `json:"yes"`
}

// check refuses a confirmation without a digest.
func (c confirmation) check() error {
	if c.Digest == "" {
		return envelope.Usage("the parameter digest is required: plan or preview the action first, then send the digest it gave")
	}
	return nil
}

// yes answers a consent or an overwrite with the confirmation's consent.
func yes[T any](c confirmation) func(T) (bool, error) {
	return func(T) (bool, error) { return c.Yes, nil }
}

func (req installRequest) options() app.InstallOptions {
	return app.InstallOptions{Variant: req.Variant, Mode: req.Mode, Harnesses: req.Harnesses, Reset: req.Reset}
}

func (req applyRequest) options() app.ApplyOptions {
	return app.ApplyOptions{Profiles: append([]string{}, req.Profiles...), Variant: req.Variant}
}

// pack validates the pack at the query parameter path, as dotai validate
// does: a pack with problems is invalid, with the result.
func (s *handler) pack(r *http.Request) (any, error) {
	dir, err := absParam(r.URL.Query().Get("path"), "path", true)
	if err != nil {
		return nil, err
	}
	res, err := s.deps.Validate(dir, app.Selection{})
	if err != nil {
		return nil, err
	}
	if !res.Valid {
		return nil, envelope.PackInvalid(res.Diagnostics, res)
	}
	return res, nil
}

// install runs dotai install as the body asks, for the plan of its digest.
func (s *handler) install(r *http.Request) (any, error) {
	var req struct {
		installRequest
		confirmation
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	pack, err := absParam(req.Pack, "pack", true)
	if err != nil {
		return nil, err
	}
	if err := req.check(); err != nil {
		return nil, err
	}
	opts := req.options()
	opts.Confirm = confirm(req.Digest, installView)
	return s.act(r, "install", func(ctx context.Context) (any, error) {
		res, err := s.deps.Install(ctx, s.cfg.CacheDir, pack, s.cfg.Home, opts, nil, nil, yes[app.InstallPlan](req.confirmation))
		if err != nil {
			return nil, err
		}
		if len(res.Diagnostics) > 0 {
			return nil, envelope.PackInvalid(res.Diagnostics, res)
		}
		return res, nil
	})
}

// apply runs dotai apply in the project of the body, for the plan of its
// digest.
func (s *handler) apply(r *http.Request) (any, error) {
	var req struct {
		applyRequest
		confirmation
	}
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
	if err := req.check(); err != nil {
		return nil, err
	}
	opts := req.options()
	opts.Confirm = confirm(req.Digest, projectView)
	return s.act(r, "apply", func(ctx context.Context) (any, error) {
		res, err := s.deps.Apply(ctx, s.cfg.CacheDir, pack, project, s.cfg.Home, opts, nil, yes[app.ProjectPlan](req.confirmation))
		if err != nil {
			return nil, err
		}
		if len(res.Diagnostics) > 0 {
			return nil, envelope.PackInvalid(res.Diagnostics, res)
		}
		return res, nil
	})
}

// restore runs dotai restore of the body's id, by default the last
// install, for the preview of its digest.
func (s *handler) restore(r *http.Request) (any, error) {
	var req struct {
		ID string `json:"id"`
		confirmation
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	if err := req.check(); err != nil {
		return nil, err
	}
	opts := app.RestoreOptions{Confirm: confirm(req.Digest, restorePreview)}
	return s.act(r, "restore", func(ctx context.Context) (any, error) {
		return s.deps.Restore(ctx, s.cfg.Home, req.ID, opts, nil, yes[app.RestoreResult](req.confirmation))
	})
}

// restoreProject runs dotai restore --project in the project of the body,
// for the preview of its digest.
func (s *handler) restoreProject(r *http.Request) (any, error) {
	var req struct {
		Project string `json:"project"`
		confirmation
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	project, err := absParam(req.Project, "project", true)
	if err != nil {
		return nil, err
	}
	if err := req.check(); err != nil {
		return nil, err
	}
	opts := app.ProjectRestoreOptions{Confirm: confirm(req.Digest, projectRestorePreview)}
	return s.act(r, "project restore", func(ctx context.Context) (any, error) {
		return s.deps.RestoreProject(ctx, project, s.cfg.Home, opts, yes[app.ProjectRestoreResult](req.confirmation))
	})
}

// fix runs dotai doctor --fix for the findings of the body's digest. The
// problems left after it make an invalid outcome with the result, as on
// the command line.
func (s *handler) fix(r *http.Request) (any, error) {
	var req struct {
		Digest string `json:"digest"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	if err := (confirmation{Digest: req.Digest}).check(); err != nil {
		return nil, err
	}
	opts := app.DoctorOptions{Fix: true, Confirm: confirm(req.Digest, doctorFindings)}
	return s.act(r, "doctor fix", func(ctx context.Context) (any, error) {
		res, err := s.deps.Doctor(ctx, s.cfg.Home, opts)
		if err != nil && res.Findings != nil {
			// A fix that failed: what was found and fixed before it
			// still tells what happened, as on the command line.
			return nil, &resultError{err: err, data: res}
		}
		if err != nil {
			return nil, err
		}
		if err := envelope.DoctorProblems(res); err != nil {
			return nil, err
		}
		return res, nil
	})
}
