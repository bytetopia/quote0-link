package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bytetopia/quote0-link/app/internal/plugin"
)

func TestParse(t *testing.T) {
	event, ok := Parse([]byte(`{"session_id":"s1","hook_event_name":"PermissionRequest"}`))
	if !ok || event.Session != "s1" || event.State != plugin.StateApproval {
		t.Fatalf("got %+v %v", event, ok)
	}
	for _, payload := range []string{`{"session_id":"s1","hook_event_name":"Other"}`, `{"hook_event_name":"Stop"}`, `nope`} {
		if _, ok := Parse([]byte(payload)); ok {
			t.Errorf("accepted %s", payload)
		}
	}
}

func TestConfigureKeepsOtherHooksAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := `{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"other"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Configure(path, "claude", `C:\Q\q.exe`, true, map[string]any{"shell": "powershell"}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	var root struct {
		Model string
		Hooks map[string][]json.RawMessage
	}
	if err := json.Unmarshal(first, &root); err != nil || root.Model != "x" {
		t.Fatalf("bad config: %s", first)
	}
	if len(root.Hooks["Stop"]) != 2 || len(root.Hooks) != len(Events) {
		t.Fatalf("unexpected hooks: %s", first)
	}
	if !strings.Contains(string(first), "emit claude --quote0-link") || !strings.Contains(string(first), `"shell": "powershell"`) {
		t.Fatalf("handler missing: %s", first)
	}
	if err := Configure(path, "claude", `C:\Q\q.exe`, true, map[string]any{"shell": "powershell"}); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(path); string(second) != string(first) {
		t.Fatal("second enable changed the file")
	}
	if err := Configure(path, "claude", `C:\Q\q.exe`, false, nil); err != nil {
		t.Fatal(err)
	}
	removed, _ := os.ReadFile(path)
	if strings.Contains(string(removed), "quote0-link") || !strings.Contains(string(removed), `"other"`) {
		t.Fatalf("disable result: %s", removed)
	}
	backups, _ := filepath.Glob(path + ".quote0-link-*.bak")
	if len(backups) != 2 {
		t.Fatalf("want 2 backups, got %d", len(backups))
	}
}
