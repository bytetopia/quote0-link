// Package plugin defines the contract between the Quote0 Link core (display,
// scheduling, local API) and pluggable integrations.
//
// There are two kinds of integration:
//
//   - Source (active / pull): the scheduler calls Fetch on a timetable and
//     shows the returned card. Examples: stock quotes, Teams presence.
//   - Hook (passive / push): an external tool runs "quote0-link emit <id>"
//     with its event payload on stdin; Parse turns it into an Event that the
//     core aggregates and shows. Examples: Codex, Claude Code.
//
// An integration lives in its own package under internal/integrations and
// registers itself from init(). internal/integrations/all imports every
// integration, so adding or removing one is a single import line there.
package plugin

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/bytetopia/quote0-link/app/internal/render"
)

// Info identifies an integration in the API and the management UI.
type Info struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note string `json:"note"`
}

// Schedule is a task timetable. Days use ISO numbering (1 = Monday); Start
// and End are local "HH:MM".
type Schedule struct {
	Days     []int  `json:"days"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Interval int    `json:"interval_minutes"`
}

// Scheduler is optionally implemented by a Source to suggest the timetable
// pre-filled for new tasks (e.g. market hours for stock quotes).
type Scheduler interface {
	DefaultSchedule() Schedule
}

// Job is what the scheduler hands to a Source on every run.
type Job struct {
	Name     string
	Settings map[string]string
}

// Source is an active integration polled by the scheduler.
type Source interface {
	Info() Info
	// Fields declares the per-task settings form; may be empty. The core
	// renders it, persists the values and passes them back in Job.Settings.
	Fields() []Field
	// Normalize receives settings already checked by Clean and returns the
	// canonical form to persist (or an error for domain-specific problems).
	Normalize(settings map[string]string) (map[string]string, error)
	// Fetch produces the card to display. It must honour ctx's deadline.
	// Return an identical card when nothing changed so the e-ink panel is
	// not refreshed needlessly.
	Fetch(ctx context.Context, job Job) (render.Card, error)
}

// Hook is a passive integration that installs itself into an external tool
// and translates that tool's callbacks into Events.
type Hook interface {
	Info() Info
	// Configure installs (enabled) or removes the hook that runs
	// "<executable> emit <id>" and returns the configuration file it touched.
	Configure(executable string, enabled bool) (path string, err error)
	// Parse reads one callback payload. Event.Source is filled in by the core.
	Parse(payload []byte) (Event, bool)
}

var (
	mu      sync.RWMutex
	sources []Source
	hooks   []Hook
)

// RegisterSource adds an active integration. It panics on invalid or
// duplicate IDs so mistakes surface at start-up.
func RegisterSource(source Source) {
	mu.Lock()
	defer mu.Unlock()
	if source == nil || source.Info().ID == "" || findSource(source.Info().ID) != nil {
		panic(fmt.Sprintf("plugin: invalid or duplicate source %v", source))
	}
	sources = append(sources, source)
}

// RegisterHook adds a passive integration.
func RegisterHook(hook Hook) {
	mu.Lock()
	defer mu.Unlock()
	if hook == nil || hook.Info().ID == "" || findHook(hook.Info().ID) != nil {
		panic(fmt.Sprintf("plugin: invalid or duplicate hook %v", hook))
	}
	hooks = append(hooks, hook)
}

// Sources returns active integrations sorted by ID.
func Sources() []Source {
	mu.RLock()
	defer mu.RUnlock()
	list := append([]Source(nil), sources...)
	slices.SortFunc(list, func(a, b Source) int { return strings.Compare(a.Info().ID, b.Info().ID) })
	return list
}

// Hooks returns passive integrations sorted by ID.
func Hooks() []Hook {
	mu.RLock()
	defer mu.RUnlock()
	list := append([]Hook(nil), hooks...)
	slices.SortFunc(list, func(a, b Hook) int { return strings.Compare(a.Info().ID, b.Info().ID) })
	return list
}

func LookupSource(id string) Source {
	mu.RLock()
	defer mu.RUnlock()
	return findSource(id)
}

func LookupHook(id string) Hook {
	mu.RLock()
	defer mu.RUnlock()
	return findHook(id)
}

func findSource(id string) Source {
	for _, source := range sources {
		if source.Info().ID == id {
			return source
		}
	}
	return nil
}

func findHook(id string) Hook {
	for _, hook := range hooks {
		if hook.Info().ID == id {
			return hook
		}
	}
	return nil
}
