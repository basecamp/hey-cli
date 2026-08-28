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

// loadCatalog derives hey's catalog from the embedded model snapshot.
func loadCatalog() (*catalog.Catalog, error) {
	model, err := fs.Sub(modelFS, "model")
	if err != nil {
		return nil, fmt.Errorf("embedded model: %w", err)
	}
	return catalog.Load(catalog.Spec{
		ToolPrefix: "hey_",
		Domains:    DomainSpecs,
		Model:      model,
	})
}
