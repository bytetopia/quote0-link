package teams

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/plugin"
)

const sample = `2026-11-23T06:04:18.29+08:00 <DBG>  TaskbarBadgeServiceLegacy:Work: SetBadge PreSetBadge verification: NumericBadge{1}, overlay: 1 items, status Busy
2026-11-23T06:05:00.00+08:00 <INF>  unrelated status line
2026-11-23T06:06:18.30+08:00 <DBG>  Window: (hwnd=0x1; tags=Main) SetTaskbarIconOverlay overlay description:3 items, status Do not disturb
`

func TestParseStatus(t *testing.T) {
	status, ok := ParseStatus(strings.NewReader(sample))
	if !ok || status.Presence != "Do not disturb" || status.Unread != 3 {
		t.Fatalf("got %+v %v", status, ok)
	}
	if _, ok := ParseStatus(strings.NewReader("nothing here\n")); ok {
		t.Fatal("found status in empty log")
	}
	if Label("Busy") != "忙碌 Busy" || Label("Custom") != "Custom" {
		t.Fatal("unexpected labels")
	}
}

func TestFetch(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "MSTeams_2026-11-23_06-00-00.00.log")
	newer := filepath.Join(dir, "MSTeams_2026-11-23_07-00-00.00.log")
	now := time.Now()
	_ = os.WriteFile(older, []byte(sample), 0600)
	_ = os.WriteFile(newer, []byte("no status yet\n"), 0600)
	_ = os.Chtimes(older, now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(newer, now, now)
	source := Source{Dirs: func() []string { return []string{dir} }, Now: func() time.Time { return now }, User: func() string { return "Ada Lovelace" }}

	card, err := source.Fetch(context.Background(), plugin.Job{})
	if err != nil || card.Body != "请勿打扰 Do not disturb\n未读 3" || card.Subtitle != "Ada Lovelace" {
		t.Fatalf("got %+v %v", card, err)
	}
	if card, _ := source.Fetch(context.Background(), plugin.Job{Settings: map[string]string{"unread": "false"}}); card.Body != "请勿打扰 Do not disturb" {
		t.Fatalf("unread hidden: %+v", card)
	}
	source.Now = func() time.Time { return now.Add(time.Hour) }
	if card, err := source.Fetch(context.Background(), plugin.Job{}); err != nil || card.Body != "未运行" {
		t.Fatalf("stale: %+v %v", card, err)
	}
	source.Dirs = func() []string { return []string{t.TempDir()} }
	if _, err := source.Fetch(context.Background(), plugin.Job{}); err == nil {
		t.Fatal("missing logs not reported")
	}
}

func TestDisplayName(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "app_settings.json")
	accounts := filepath.Join(dir, "accounts")
	_ = os.Mkdir(accounts, 0700)
	_ = os.WriteFile(settings, []byte(`{"active_users":[{"id":"../x"},{"id":"missing"},{"id":"abc","state":5}]}`), 0600)
	_ = os.WriteFile(filepath.Join(accounts, "abc"), []byte(`{"display_name":" Ada Lovelace ","email":"ada@example.com"}`), 0600)
	if got := DisplayName(settings, accounts); got != "Ada Lovelace" {
		t.Fatalf("got %q", got)
	}
	if got := DisplayName(filepath.Join(dir, "none.json"), accounts); got != "" {
		t.Fatalf("missing settings: %q", got)
	}
}
