// Package all links every built-in integration into the binary. Each
// integration registers itself with the plugin registry from its init
// function; add or remove a line here to plug one in or out.
package all

import (
	_ "github.com/bytetopia/quote0-link/app/internal/integrations/claude"
	_ "github.com/bytetopia/quote0-link/app/internal/integrations/codex"
	_ "github.com/bytetopia/quote0-link/app/internal/integrations/stock"
	_ "github.com/bytetopia/quote0-link/app/internal/integrations/teams"
)
