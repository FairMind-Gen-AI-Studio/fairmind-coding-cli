// Package assets embeds the Copilot integration files (skill, custom agent,
// repository-instructions block, bundled tool reference) so `fairmind setup`
// can scaffold a repository without needing this source tree at hand.
//
// The canonical copies live under github/ at the repo root; run
// `make sync-templates` to refresh templates/ from them.
package assets

import "embed"

//go:embed templates
var FS embed.FS

// Root is the embed path prefix for the templates.
const Root = "templates"
