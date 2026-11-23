// Package codex is the passive integration for OpenAI Codex hooks.
package codex

import (
	"path/filepath"

	"github.com/bytetopia/quote0-link/app/internal/integrations/agenthook"
	"github.com/bytetopia/quote0-link/app/internal/plugin"
)

func init() { plugin.RegisterHook(hook{}) }

type hook struct{}

func (hook) Info() plugin.Info {
	return plugin.Info{ID: "codex", Name: "Codex", Note: "安装后，在 Codex 中审查并信任 Hook。"}
}

func (hook) Configure(executable string, enabled bool) (string, error) {
	dir, err := agenthook.ConfigDir("CODEX_HOME", ".codex")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "hooks.json")
	return path, agenthook.Configure(path, "codex", executable, enabled, nil)
}

func (hook) Parse(payload []byte) (plugin.Event, bool) { return agenthook.Parse(payload) }
