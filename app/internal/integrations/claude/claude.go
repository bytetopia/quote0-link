// Package claude is the passive integration for Claude Code hooks.
package claude

import (
	"path/filepath"
	"runtime"

	"github.com/bytetopia/quote0-link/app/internal/integrations/agenthook"
	"github.com/bytetopia/quote0-link/app/internal/plugin"
)

func init() { plugin.RegisterHook(hook{}) }

type hook struct{}

func (hook) Info() plugin.Info {
	return plugin.Info{ID: "claude", Name: "Claude Code", Note: "安装后，启动新会话以加载用户级 Hook。"}
}

func (hook) Configure(executable string, enabled bool) (string, error) {
	dir, err := agenthook.ConfigDir("CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "settings.json")
	var extra map[string]any
	if runtime.GOOS == "windows" {
		extra = map[string]any{"shell": "powershell"}
	}
	return path, agenthook.Configure(path, "claude", executable, enabled, extra)
}

func (hook) Parse(payload []byte) (plugin.Event, bool) { return agenthook.Parse(payload) }
