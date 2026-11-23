// Package teams is an active integration that shows the local Microsoft Teams
// presence. It reads the new Teams client's own diagnostic log, so it needs no
// sign-in, Graph permissions or network access.
package teams

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/plugin"
	"github.com/bytetopia/quote0-link/app/internal/render"
)

const (
	tailBytes = 4 << 20
	maxFiles  = 4
	// The client writes its log many times a minute while running.
	staleAfter = 20 * time.Minute
)

var (
	// "...SetTaskbarIconOverlay overlay description:1 items, status Busy"
	// "...SetBadge ...: NumericBadge{1}, overlay: 1 items, status Do not disturb"
	statusLine = regexp.MustCompile(`(\d+) items, status ([A-Za-z][A-Za-z ]*?)\s*$`)
	labels     = map[string]string{
		"Available": "可用", "Busy": "忙碌", "Do not disturb": "请勿打扰", "Away": "离开",
		"Be right back": "马上回来", "Offline": "脱机", "Unknown": "未知", "In a meeting": "会议中",
		"In a call": "通话中", "Presenting": "演示中", "Focusing": "专注中", "Out of office": "不在办公室",
	}
)

func init() { plugin.RegisterSource(Source{Dirs: logDirs, Now: time.Now, User: userName}) }

type Source struct {
	Dirs func() []string
	Now  func() time.Time
	// User returns the signed-in display name, or "" when unknown.
	User func() string
}

func (Source) Info() plugin.Info {
	return plugin.Info{ID: "teams", Name: "Teams 状态", Note: "读取本机新版 Microsoft Teams 客户端日志中的在线状态和未读数，并以本机登录账户的显示名作为副标题，无需登录或授权。"}
}

func (Source) Fields() []plugin.Field {
	return []plugin.Field{{Key: "unread", Label: "显示未读数", Type: plugin.Toggle, Default: "true"}}
}

func (Source) DefaultSchedule() plugin.Schedule {
	return plugin.Schedule{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "18:00", Interval: 2}
}

func (Source) Normalize(settings map[string]string) (map[string]string, error) { return settings, nil }

// Status is the presence found in the log.
type Status struct {
	Presence string
	Unread   int
}

func (source Source) Fetch(ctx context.Context, job plugin.Job) (render.Card, error) {
	files := logFiles(source.Dirs())
	if len(files) == 0 {
		return render.Card{}, errors.New("未找到新版 Teams 客户端日志")
	}
	card := render.Card{Template: "card", Title: "Microsoft Teams", Subtitle: "在线状态"}
	if source.User != nil {
		if name := source.User(); name != "" {
			card.Subtitle = name
		}
	}
	if info, err := os.Stat(files[0]); err == nil && source.Now().Sub(info.ModTime()) > staleAfter {
		card.Body = "未运行"
		return card, nil
	}
	for _, file := range files[:min(len(files), maxFiles)] {
		if err := ctx.Err(); err != nil {
			return render.Card{}, err
		}
		status, ok, err := readStatus(file)
		if err != nil {
			return render.Card{}, err
		}
		if ok {
			card.Body = Label(status.Presence)
			if job.Settings["unread"] != "false" {
				card.Body += "\n未读 " + strconv.Itoa(status.Unread)
			}
			return card, nil
		}
	}
	return render.Card{}, errors.New("Teams 日志中暂无状态记录")
}

// Label renders "忙碌 Busy" for known presences and the raw value otherwise.
func Label(presence string) string {
	if label, ok := labels[presence]; ok {
		return label + " " + presence
	}
	return presence
}

// ParseStatus returns the last presence recorded in a log excerpt.
func ParseStatus(log io.Reader) (Status, bool) {
	var status Status
	found := false
	scanner := bufio.NewScanner(log)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte("status ")) {
			continue
		}
		if match := statusLine.FindSubmatch(line); match != nil {
			unread, _ := strconv.Atoi(string(match[1]))
			status, found = Status{string(match[2]), unread}, true
		}
	}
	return status, found
}

func readStatus(path string) (Status, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return Status{}, false, err
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.Size() > tailBytes {
		if _, err := file.Seek(-tailBytes, io.SeekEnd); err != nil {
			return Status{}, false, err
		}
	}
	status, ok := ParseStatus(file)
	return status, ok, nil
}

func logFiles(dirs []string) []string {
	type item struct {
		path string
		at   time.Time
	}
	var items []item
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "MSTeams_*.log"))
		for _, path := range matches {
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				items = append(items, item{path, info.ModTime()})
			}
		}
	}
	slices.SortFunc(items, func(a, b item) int { return b.at.Compare(a.at) })
	paths := make([]string, len(items))
	for index, item := range items {
		paths[index] = item.path
	}
	return paths
}

func logDirs() []string {
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		return []string{filepath.Join(home, "Library", "Group Containers", "UBF8T346G9.com.microsoft.teams", "Library", "Application Support", "Logs")}
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return nil
	}
	return []string{filepath.Join(local, "Packages", "MSTeams_8wekyb3d8bbwe", "LocalCache", "Microsoft", "MSTeams", "Logs")}
}

// userName resolves the Teams account's display name without network access:
// Teams lists its signed-in account IDs in app_settings.json and the shared
// Microsoft OneAuth cache keeps a profile file per account ID.
func userName() string {
	local := os.Getenv("LOCALAPPDATA")
	if runtime.GOOS != "windows" || local == "" {
		return ""
	}
	return DisplayName(
		filepath.Join(local, "Packages", "MSTeams_8wekyb3d8bbwe", "LocalCache", "Microsoft", "MSTeams", "app_settings.json"),
		filepath.Join(local, "Microsoft", "OneAuth", "accounts"),
	)
}

// DisplayName returns the display name of the first active Teams account
// found in the OneAuth accounts directory.
func DisplayName(settingsPath, accountsDir string) string {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return ""
	}
	var settings struct {
		ActiveUsers []struct {
			ID string `json:"id"`
		} `json:"active_users"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return ""
	}
	for _, user := range settings.ActiveUsers {
		if user.ID == "" || filepath.Base(user.ID) != user.ID {
			continue
		}
		data, err := os.ReadFile(filepath.Join(accountsDir, user.ID))
		if err != nil {
			continue
		}
		var account struct {
			DisplayName string `json:"display_name"`
		}
		if json.Unmarshal(data, &account) == nil {
			if name := strings.TrimSpace(account.DisplayName); name != "" {
				return name
			}
		}
	}
	return ""
}
