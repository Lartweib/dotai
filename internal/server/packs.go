package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

// The pack registry of the home (~/.dotai/packs.json, decision 0044), the
// copy of a pack and what each layer of a pack holds. Adding, updating,
// removing and copying write, so each runs as one operation, as an action
// does. git never prompts from here: a repository that needs an answer is
// added once from the command line.

type packPath struct {
	Path string `json:"path"`
}

func (s *handler) packs(*http.Request) (any, error) { return s.deps.ListPacks(s.cfg.Home) }

// importPack adds the local pack of the body, as pack add --dir does, once
// it validates.
func (s *handler) importPack(r *http.Request) (any, error) {
	var req packPath
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	dir, err := absParam(req.Path, "path", true)
	if err != nil {
		return nil, err
	}
	return s.act(r, "pack add", func(context.Context) (any, error) { return s.deps.AddLocalPack(s.cfg.Home, dir) })
}

// addPack fetches the git pack of the body and adds it, as pack add does.
// A pack already added is an update: without digest, it fails with
// consent-required and the plan with its digest, which the page shows and
// sends back to carry that plan out.
func (s *handler) addPack(r *http.Request) (any, error) {
	var req struct {
		URL    string `json:"url"`
		Ref    string `json:"ref"`
		Digest string `json:"digest"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	if req.URL == "" {
		return nil, envelope.Usage("the parameter url is required")
	}
	opts := app.PackGitOptions{Ref: req.Ref, Consent: consentBy(req.Digest)}
	if req.Digest != "" {
		opts.Confirm = confirm(req.Digest, updateView)
	}
	return s.act(r, "pack add", func(ctx context.Context) (any, error) {
		return planToConfirm(s.deps.AddGitPack(ctx, s.cfg.Home, req.URL, opts))
	})
}

// planUpdate plans pack update of the body's identity: it fetches and
// checks into a temporary directory, and gives the plan with its digest.
func (s *handler) planUpdate(r *http.Request) (any, error) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, envelope.Usage("the parameter id is required")
	}
	return s.plan(r, "pack update plan", func(ctx context.Context) (any, error) {
		p, notices, err := s.deps.PlanPackUpdate(ctx, s.cfg.Home, req.ID, app.PackGitOptions{})
		if err != nil {
			return nil, tooLong(err)
		}
		view, err := updateView(p)
		if err != nil {
			return nil, err
		}
		// What went wrong once the plan was made, such as a temporary fetch
		// that could not be deleted, is outside the plan and its digest.
		return struct {
			packUpdateView
			Notices []app.PackWarning `json:"notices"`
		}{view.(packUpdateView), notices}, nil
	})
}

// updatePack runs pack update of the body's identity for the plan of its
// digest, which is the consent of --yes.
func (s *handler) updatePack(r *http.Request) (any, error) {
	var req struct {
		ID     string `json:"id"`
		Digest string `json:"digest"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, envelope.Usage("the parameter id is required")
	}
	if err := (confirmation{Digest: req.Digest}).check(); err != nil {
		return nil, err
	}
	opts := app.PackGitOptions{Confirm: confirm(req.Digest, updateView), Consent: consentBy(req.Digest)}
	return s.act(r, "pack update", func(ctx context.Context) (any, error) {
		return planToConfirm(s.deps.UpdatePack(ctx, s.cfg.Home, req.ID, opts))
	})
}

// consentBy is the consent of an update: the digest of the plan the user
// saw, once Confirm checked it is the plan carried out.
func consentBy(digest string) func(app.PackUpdatePlan) (bool, error) {
	return func(app.PackUpdatePlan) (bool, error) { return digest != "", nil }
}

// planToConfirm gives the plan of an update without consent with its
// digest, so that the page can show it and confirm it.
func planToConfirm(res app.PackAddResult, err error) (any, error) {
	if ce, ok := errors.AsType[*app.PackConsentError](err); ok {
		view, verr := updateView(ce.Plan)
		if verr != nil {
			return nil, verr
		}
		return nil, &apiError{code: envelope.CodeConsent, status: http.StatusConflict, data: view,
			msg: "this pack is already added, so adding it again updates it: review the plan, then confirm it with its digest"}
	}
	if err != nil {
		return nil, tooLong(err)
	}
	return res, nil
}

// tooLong points a fetch stopped by the time limit of the operation to the
// command line, whose fetch may take ten minutes.
func tooLong(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w; a large repository may need longer than the web UI allows: add or update it with dotai pack add or dotai pack update", err)
	}
	return err
}

// removePack removes the pack of the body from the registry, by its
// identity or, for an entry without one or written by hand, by its path
// as the registry lists it. A git pack's clone in ~/.dotai/packs is
// deleted; a local pack's files stay.
func (s *handler) removePack(r *http.Request) (any, error) {
	var req struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	switch {
	case req.ID == "" && req.Path == "":
		return nil, envelope.Usage("the parameter id or path is required")
	case req.ID != "" && req.Path != "":
		return nil, envelope.Usage("give the parameter id or path, not both")
	}
	return s.act(r, "pack removal", func(context.Context) (any, error) {
		if req.ID != "" {
			return s.deps.RemovePackID(s.cfg.Home, req.ID)
		}
		return s.deps.RemovePack(s.cfg.Home, req.Path)
	})
}

// copyPack copies the pack from to the new directory to, without
// importing it.
func (s *handler) copyPack(r *http.Request) (any, error) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	from, err := absParam(req.From, "from", true)
	if err != nil {
		return nil, err
	}
	to, err := absParam(req.To, "to", true)
	if err != nil {
		return nil, err
	}
	return s.act(r, "pack copy", func(ctx context.Context) (any, error) { return s.deps.CopyPack(ctx, from, to) })
}

// packSummary lists what each layer of the pack at the query parameter
// path holds, by type.
func (s *handler) packSummary(r *http.Request) (any, error) {
	dir, err := absParam(r.URL.Query().Get("path"), "path", true)
	if err != nil {
		return nil, err
	}
	return s.deps.SummarizePack(dir)
}
