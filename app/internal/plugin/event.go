package plugin

import "time"

// Agent states understood by the core. Hooks map their tool's events to these.
const (
	StateWorking  = "working"
	StateApproval = "approval"
	StateFinished = "finished"
	StateClosed   = "closed"
)

// Event is one callback from a passive integration, as posted to /v1/events.
type Event struct {
	Source  string `json:"source"`
	Session string `json:"session"`
	State   string `json:"state"`
}

// Valid reports whether the event is well formed and from a registered hook.
func (event Event) Valid() bool {
	if event.Session == "" || len(event.Session) > 256 {
		return false
	}
	switch event.State {
	case StateWorking, StateApproval, StateFinished, StateClosed:
	default:
		return false
	}
	return LookupHook(event.Source) != nil
}

type entry struct {
	event Event
	at    time.Time
}

// Sessions tracks concurrent agent sessions so the display shows the most
// important one (approval > working > finished, newest first).
type Sessions map[[2]string]entry

func (sessions Sessions) Apply(event Event, now time.Time) Event {
	key := [2]string{event.Source, event.Session}
	if event.State == StateClosed {
		delete(sessions, key)
	} else {
		sessions[key] = entry{event, now}
	}
	var selected entry
	rank := map[string]int{StateApproval: 3, StateWorking: 2, StateFinished: 1}
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
