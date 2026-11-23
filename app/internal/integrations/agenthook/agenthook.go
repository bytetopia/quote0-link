// Package agenthook implements the "hooks" JSON format shared by Codex and
// Claude Code, so each of those integrations only supplies its config path
// and a few options.
package agenthook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/plugin"
)

// Events maps the agent's hook event names to core states.
var Events = map[string]string{
	"UserPromptSubmit":  plugin.StateWorking,
	"PermissionRequest": plugin.StateApproval,
	"PostToolUse":       plugin.StateWorking,
	"Stop":              plugin.StateFinished,
	"SessionEnd":        plugin.StateClosed,
}

// Parse reads a hook payload carrying session_id and hook_event_name.
func Parse(payload []byte) (plugin.Event, bool) {
	var input struct {
		Session string `json:"session_id"`
		Event   string `json:"hook_event_name"`
	}
	if json.Unmarshal(payload, &input) != nil {
		return plugin.Event{}, false
	}
	state, ok := Events[input.Event]
	return plugin.Event{Session: input.Session, State: state}, ok && input.Session != ""
}

// Configure adds or removes the Quote0 Link handler for every event in
// Events. extra is merged into the command action (for example a shell).
// The original file is backed up before any change.
func Configure(path, source, executable string, enabled bool, extra map[string]any) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("expected a regular configuration file; original configuration left untouched")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if !enabled {
			return nil
		}
		data = []byte("{}")
	} else if err != nil {
		return err
	}
	next, changed, err := merge(data, source, executable, enabled, extra)
	if err != nil || !changed {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		backup := path + ".quote0-link-" + time.Now().Format("20060102-150405.000000000") + ".bak"
		if err := os.WriteFile(backup, data, 0600); err != nil {
			return err
		}
	}
	return plugin.AtomicWrite(path, next)
}

// ConfigDir returns $env or ~/fallback.
func ConfigDir(env, fallback string) (string, error) {
	if dir := os.Getenv(env); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, fallback), nil
}

func merge(data []byte, source, executable string, enabled bool, extra map[string]any) ([]byte, bool, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &root); err != nil || root == nil {
		return nil, false, errors.New("expected a JSON object; original configuration left untouched")
	}
	hooks := map[string]json.RawMessage{}
	if raw, exists := root["hooks"]; exists {
		if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
			return nil, false, errors.New("expected a hooks object; original configuration left untouched")
		}
	}
	marker := " emit " + source + " --quote0-link"
	command, err := shellCommand(executable, marker)
	if err != nil {
		return nil, false, err
	}
	action := map[string]any{"type": "command", "command": command, "timeout": 2}
	for key, value := range extra {
		action[key] = value
	}
	handler, _ := json.Marshal(map[string]any{"hooks": []any{action}})
	changed := false
	for name := range Events {
		var groups []json.RawMessage
		if raw, ok := hooks[name]; ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return nil, false, fmt.Errorf("invalid %s hooks: %w", name, err)
			}
		}
		kept := make([]json.RawMessage, 0, len(groups)+1)
		count := 0
		correct := false
		for _, raw := range groups {
			var group struct {
				Hooks []struct {
					Type    string
					Command string
					Timeout int
				}
			}
			if json.Unmarshal(raw, &group) == nil && len(group.Hooks) == 1 &&
				group.Hooks[0].Type == "command" && strings.HasSuffix(group.Hooks[0].Command, marker) {
				count++
				correct = group.Hooks[0].Command == command && group.Hooks[0].Timeout == 2
				continue
			}
			kept = append(kept, raw)
		}
		if (enabled && count == 1 && correct) || (!enabled && count == 0) {
			continue
		}
		changed = true
		if enabled {
			kept = append(kept, handler)
		}
		if len(kept) == 0 {
			delete(hooks, name)
		} else {
			hooks[name], _ = json.Marshal(kept)
		}
	}
	if !changed {
		return data, false, nil
	}
	root["hooks"], _ = json.Marshal(hooks)
	result, err := json.MarshalIndent(root, "", "  ")
	return append(result, '\n'), true, err
}

func shellCommand(executable, marker string) (string, error) {
	if strings.ContainsAny(executable, "\r\n") {
		return "", errors.New("executable path contains a line break")
	}
	if runtime.GOOS != "windows" {
		executable = filepath.ToSlash(executable)
		return "'" + strings.ReplaceAll(executable, "'", `'"'"'`) + "'" + marker, nil
	}
	return "& '" + strings.ReplaceAll(executable, "'", "''") + "'" + marker, nil
}
