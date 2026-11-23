package schedule

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/plugin"
	"github.com/bytetopia/quote0-link/app/internal/render"
)

type fake struct{}

func (fake) Info() plugin.Info { return plugin.Info{ID: "fake", Name: "Fake"} }
func (fake) Fields() []plugin.Field {
	return []plugin.Field{{Key: "word", Label: "词", Required: true}}
}
func (fake) Normalize(settings map[string]string) (map[string]string, error) {
	if settings["word"] == "bad" {
		return nil, errors.New("bad word")
	}
	return map[string]string{"word": strings.ToUpper(settings["word"])}, nil
}
func (fake) Fetch(_ context.Context, job plugin.Job) (render.Card, error) {
	return render.Card{Template: "card", Title: job.Name, Body: job.Settings["word"]}, nil
}

func init() { plugin.RegisterSource(fake{}) }

func at(day, clock string) time.Time {
	value, _ := time.ParseInLocation("2006-01-02 15:04", day+" "+clock, time.Local)
	return value
}

func TestDue(t *testing.T) {
	// 2026-11-23 is a Monday.
	task := Task{Enabled: true, Days: []int{1, 2, 3, 4, 5}, Start: "09:30", End: "15:00", Interval: 5}
	cases := []struct {
		name      string
		now, last time.Time
		want      bool
	}{
		{"first run in window", at("2026-11-23", "09:31"), time.Time{}, true},
		{"before window", at("2026-11-23", "09:29"), time.Time{}, false},
		{"window end is exclusive", at("2026-11-23", "15:00"), time.Time{}, false},
		{"same slot already ran", at("2026-11-23", "09:34"), at("2026-11-23", "09:31"), false},
		{"next slot", at("2026-11-23", "09:35"), at("2026-11-23", "09:31"), true},
		{"weekend", at("2026-11-28", "10:00"), time.Time{}, false},
	}
	for _, test := range cases {
		if got := task.Due(test.now, test.last); got != test.want {
			t.Errorf("%s: got %v", test.name, got)
		}
	}
	night := Task{Enabled: true, Days: []int{5}, Start: "22:00", End: "02:00", Interval: 60}
	if !night.Due(at("2026-11-28", "01:10"), time.Time{}) { // Saturday morning, window opened Friday
		t.Error("cross-midnight window not honoured")
	}
	if night.Due(at("2026-11-27", "01:10"), time.Time{}) { // Friday morning, window opened Thursday
		t.Error("ran on a window opened on an unselected day")
	}
	allDay := Task{Enabled: true, Days: []int{1}, Start: "00:00", End: "00:00", Interval: 30}
	if !allDay.Due(at("2026-11-23", "23:59"), time.Time{}) {
		t.Error("equal start and end should mean all day")
	}
	if (Task{Days: []int{1}, Start: "00:00", End: "00:00", Interval: 1}).Due(at("2026-11-23", "12:00"), time.Time{}) {
		t.Error("disabled task is due")
	}
}

func TestSaveRunDeletePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	var shown []render.Card
	scheduler, err := New(path, func(card render.Card) error { shown = append(shown, card); return nil })
	if err != nil {
		t.Fatal(err)
	}
	base := Task{Type: "fake", Enabled: true, Days: []int{7, 1, 1}, Start: "08:00", End: "18:00", Interval: 10}
	for _, bad := range []Task{
		{Type: "nope", Days: []int{1}, Start: "08:00", End: "09:00", Interval: 1},
		func() Task { task := base; task.Settings = map[string]string{"word": "bad"}; return task }(),
		base, // required field missing
		func() Task {
			task := base
			task.Days = []int{8}
			task.Settings = map[string]string{"word": "x"}
			return task
		}(),
		func() Task {
			task := base
			task.Interval = 0
			task.Settings = map[string]string{"word": "x"}
			return task
		}(),
	} {
		if _, err := scheduler.Save(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	base.Settings = map[string]string{"word": " hi ", "unknown": "dropped"}
	saved, err := scheduler.Save(base)
	if err != nil || saved.ID == "" || saved.Name != "Fake" || saved.Settings["word"] != "HI" || len(saved.Settings) != 1 || len(saved.Days) != 2 {
		t.Fatalf("got %+v %v", saved, err)
	}
	view, err := scheduler.RunNow(context.Background(), saved.ID)
	if err != nil || len(shown) != 1 || shown[0].Body != "HI" || view.Result == "" {
		t.Fatalf("run: %+v %v %v", view, err, shown)
	}
	saved.Type = "other"
	if _, err := scheduler.Save(saved); err == nil {
		t.Fatal("type change accepted")
	}

	reloaded, err := New(path, nil)
	if err != nil || len(reloaded.List()) != 1 || reloaded.List()[0].Settings["word"] != "HI" {
		t.Fatalf("reload: %+v %v", reloaded.List(), err)
	}
	if err := reloaded.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := New(path, nil); len(again.List()) != 0 {
		t.Fatal("delete not persisted")
	}
	if kinds := Kinds(); len(kinds) != 1 || kinds[0].ID != "fake" || len(kinds[0].Fields) != 1 {
		t.Fatalf("kinds: %+v", kinds)
	}
}
