// Package mcpserver assembles the MCP server behind `hey mcp`: hey's tool
// catalog derived from hey-sdk's model exports, dispatched through the CLI's
// authenticated SDK client.
//
// The generic machinery — joining behavior-model.json with openapi.json,
// rendering domain gateway tools, action dispatch, read-only filtering, the
// in-band describe action — lives in the shared toolkit at
// github.com/basecamp/mcp. This package supplies the product half: the
// curated DomainSpecs mapping hey-sdk tags to domains, the vendored model
// snapshot under model/ (synced by scripts/sync-mcp-model.sh, provenance
// recorded), and the dispatcher that turns catalog operations into hey-sdk
// requests.
package mcpserver

import (
	"embed"
	"fmt"
	"io/fs"

	"github.com/basecamp/mcp/catalog"
)

//go:embed model/behavior-model.json model/openapi.json
var modelFS embed.FS

// loadCatalog derives hey's catalog from the embedded model snapshot, with
// the blurbs a read-only server serves when readOnly (domainSpecs).
func loadCatalog(readOnly bool) (*catalog.Catalog, error) {
	model, err := fs.Sub(modelFS, "model")
	if err != nil {
		return nil, fmt.Errorf("embedded model: %w", err)
	}
	cat, err := catalog.Load(catalog.Spec{
		ToolPrefix: "hey_",
		Domains:    domainSpecs(readOnly),
		Model:      model,
	})
	if err != nil {
		return nil, err
	}
	hideWireMechanics(cat)
	return cat, nil
}

// hideWireMechanics drops each operation's HTTP pagination and retry policy
// from what describe serves. They are how this server talks to HEY, not how a
// client talks to it: a paged read answers its next page as a cursor in the
// result (dispatch.go), never as the Link or X-Total-Count headers the model
// describes, and every write goes out once (the CLI's single-shot client), so
// a client that followed a write's retry policy could save or toggle twice.
// Paginated stays: it says a read has more pages.
func hideWireMechanics(cat *catalog.Catalog) {
	for _, d := range cat.Domains {
		for _, op := range d.Operations {
			op.Pagination = nil
			op.Retry = nil
		}
	}
}
