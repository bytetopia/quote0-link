package integration

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
)

type Adapter struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
}

var Catalog = []Adapter{
	{"codex", "Codex", "安装后，在 Codex 中审查并信任 Hook。"},
	{"claude", "Claude Code", "安装后，启动新会话以加载用户级 Hook。"},
}

var events = map[string]string{
	"UserPromptSubmit":  "working",
	"PermissionRequest": "approval",
	"PostToolUse":       "working",
	"Stop":              "finished",
	"SessionEnd":        "closed",
}

type Event struct {
	Source  string `json:"source"`
	Session string `json:"session"`
	State   string `json:"state"`
}

func ReadEvent(source string, payload []byte) (Event, bool) {
	var input struct {
		Session string `json:"session_id"`
		Event   string `json:"hook_event_name"`
	}
	if json.Unmarshal(payload, &input) != nil {
		return Event{}, false
	}
	event := Event{source, input.Session, events[input.Event]}
	return event, event.Valid()
}

func (event Event) Valid() bool {
	if event.Session == "" || len(event.Session) > 256 {
		return false
	}
	switch event.State {
	case "working", "approval", "finished", "closed":
	default:
		return false
	}
	for _, adapter := range Catalog {
		if adapter.ID == event.Source {
			return true
		}
	}
	return false
}

type entry struct {
	event Event
	at    time.Time
}

type Sessions map[[2]string]entry

func (sessions Sessions) Apply(event Event, now time.Time) Event {
	key := [2]string{event.Source, event.Session}
	if event.State == "closed" {
		delete(sessions, key)
	} else {
		sessions[key] = entry{event, now}
	}
	var selected entry
	rank := map[string]int{"approval": 3, "working": 2, "finished": 1}
	for key, current := range sessions {
		if now.Sub(current.at) > time.Hour {
			delete(sessions, key)
			continue
		}
		if rank[current.event.State] > rank[selected.event.State] ||
			(rank[current.event.State] == rank[selected.event.State] && current.at.After(selected.at)) {
			selected = current
		}
	}
	return selected.event
}

func Path(source string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch source {
	case "codex":
		dir := os.Getenv("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		return filepath.Join(dir, "hooks.json"), nil
	case "claude":
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		if dir == "" {
			dir = filepath.Join(home, ".claude")
		}
		return filepath.Join(dir, "settings.json"), nil
	default:
		return "", errors.New("unknown integration")
	}
}

func Configure(path, source, executable string, enabled bool) error {
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
	next, changed, err := merge(data, source, executable, enabled)
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
	return AtomicWrite(path, next)
}

func merge(data []byte, source, executable string, enabled bool) ([]byte, bool, error) {
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
	if runtime.GOOS == "windows" && source == "claude" {
		action["shell"] = "powershell"
	}
	handler, _ := json.Marshal(map[string]any{"hooks": []any{action}})
	changed := false
	for name := range events {
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

func AtomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".quote0-link-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
