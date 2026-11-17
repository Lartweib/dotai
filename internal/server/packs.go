package server

import (
	"context"
	"net/http"

	"github.com/lartweib/dotai/internal/envelope"
)

// The pack registry of the home (~/.dotai/packs.json), the copy of a pack
// and what each layer of a pack holds. Import, removal and copy write, so
// each runs as one operation, as an action does.

type packPath struct {
	Path string `json:"path"`
}

func (s *handler) packs(*http.Request) (any, error) { return s.deps.ListPacks(s.cfg.Home) }

// importPack adds the pack of the body to the registry once it validates.
func (s *handler) importPack(r *http.Request) (any, error) {
	var req packPath
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	dir, err := absParam(req.Path, "path", true)
	if err != nil {
		return nil, err
	}
	return s.act(r, "pack import", func(context.Context) (any, error) { return s.deps.AddLocalPack(s.cfg.Home, dir) })
}

// removePack forgets the pack of the body; its files stay.
func (s *handler) removePack(r *http.Request) (any, error) {
	var req packPath
	if err := decodeJSON(r, &req); err != nil {
		return nil, err
	}
	// A path that is not absolute can be an entry of a registry written by
	// hand, which the removal drops as it is written.
	dir := req.Path
	if dir == "" {
		return nil, envelope.Usage("the parameter path is required")
	}
	return s.act(r, "pack removal", func(context.Context) (any, error) { return s.deps.RemovePack(s.cfg.Home, dir) })
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
