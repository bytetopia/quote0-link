// Package schedule runs the "active" integrations: tasks that the app polls on
// a weekly timetable and pushes to the display.
package schedule

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bytetopia/quote0-link/app/internal/plugin"
	"github.com/bytetopia/quote0-link/app/internal/render"
)

const (
	maxTasks   = 32
	runTimeout = 8 * time.Second
)

// Kind describes a task type (a registered plugin.Source) for the UI.
type Kind struct {
	plugin.Info
	Fields   []plugin.Field  `json:"fields"`
	Schedule plugin.Schedule `json:"schedule"`
}

var defaultSchedule = plugin.Schedule{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "18:00", Interval: 5}

// Kinds lists the task types available in this build.
func Kinds() []Kind {
	kinds := []Kind{}
	for _, source := range plugin.Sources() {
		fields := source.Fields()
		if fields == nil {
			fields = []plugin.Field{}
		}
		schedule := defaultSchedule
		if scheduler, ok := source.(plugin.Scheduler); ok {
			schedule = scheduler.DefaultSchedule()
		}
		kinds = append(kinds, Kind{source.Info(), fields, schedule})
	}
	return kinds
}

// Task is one scheduled job. Days use ISO numbering: 1 = Monday … 7 = Sunday.
// Start and End are local "HH:MM"; an End earlier than Start crosses midnight
// and equal values mean the whole day.
type Task struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Name     string            `json:"name"`
	Enabled  bool              `json:"enabled"`
	Days     []int             `json:"days"`
	Start    string            `json:"start"`
	End      string            `json:"end"`
	Interval int               `json:"interval_minutes"`
	Settings map[string]string `json:"settings,omitempty"`
}

// State is the outcome of the most recent run.
type State struct {
	LastRun time.Time `json:"last_run,omitzero"`
	Result  string    `json:"result,omitempty"`
	Error   string    `json:"error,omitempty"`
	Running bool      `json:"running,omitempty"`
}

type View struct {
	Task
	State
}

type Scheduler struct {
	path string
	show func(render.Card) error
	now  func() time.Time

	mu    sync.Mutex
	tasks []Task
	state map[string]*State
}

// New loads the task list from path (a missing file means no tasks). show is
// called with every card a task produces.
func New(path string, show func(render.Card) error) (*Scheduler, error) {
	scheduler := &Scheduler{
		path: path, show: show, now: time.Now, state: map[string]*State{},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return scheduler, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for _, task := range file.Tasks {
		if task, err := task.normalized(); err == nil && task.ID != "" {
			scheduler.tasks = append(scheduler.tasks, task)
		}
	}
	return scheduler, nil
}

func (scheduler *Scheduler) List() []View {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	views := make([]View, 0, len(scheduler.tasks))
	for _, task := range scheduler.tasks {
		view := View{Task: task}
		if state := scheduler.state[task.ID]; state != nil {
			view.State = *state
		}
		views = append(views, view)
	}
	return views
}

// Save creates (empty ID) or replaces a task and persists the list.
func (scheduler *Scheduler) Save(task Task) (Task, error) {
	task, err := task.normalized()
	if err != nil {
		return Task{}, err
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	tasks := slices.Clone(scheduler.tasks)
	if task.ID == "" {
		if len(tasks) >= maxTasks {
			return Task{}, fmt.Errorf("最多 %d 个任务", maxTasks)
		}
		var random [6]byte
		if _, err := rand.Read(random[:]); err != nil {
			return Task{}, err
		}
		task.ID = hex.EncodeToString(random[:])
		tasks = append(tasks, task)
	} else {
		index := slices.IndexFunc(tasks, func(item Task) bool { return item.ID == task.ID })
		if index < 0 {
			return Task{}, errors.New("任务不存在")
		}
		if tasks[index].Type != task.Type {
			return Task{}, errors.New("不能修改任务类型")
		}
		tasks[index] = task
	}
	if err := scheduler.persist(tasks); err != nil {
		return Task{}, err
	}
	scheduler.tasks = tasks
	// A changed timetable or symbol list should take effect on the next tick.
	if state := scheduler.state[task.ID]; state != nil {
		state.LastRun = time.Time{}
	}
	return task, nil
}

func (scheduler *Scheduler) Delete(id string) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	tasks := slices.DeleteFunc(slices.Clone(scheduler.tasks), func(item Task) bool { return item.ID == id })
	if len(tasks) == len(scheduler.tasks) {
		return errors.New("任务不存在")
	}
	if err := scheduler.persist(tasks); err != nil {
		return err
	}
	scheduler.tasks = tasks
	delete(scheduler.state, id)
	return nil
}

func (scheduler *Scheduler) persist(tasks []Task) error {
	data, err := json.MarshalIndent(struct {
		Tasks []Task `json:"tasks"`
	}{tasks}, "", "  ")
	if err != nil {
		return err
	}
	return plugin.AtomicWrite(scheduler.path, append(data, '\n'))
}

// Run checks the timetable every few seconds until ctx is cancelled.
func (scheduler *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		now := scheduler.now()
		for _, task := range scheduler.due(now) {
			_, _ = scheduler.execute(ctx, task, now)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunNow executes a task immediately, ignoring its timetable.
func (scheduler *Scheduler) RunNow(ctx context.Context, id string) (View, error) {
	scheduler.mu.Lock()
	index := slices.IndexFunc(scheduler.tasks, func(item Task) bool { return item.ID == id })
	if index < 0 {
		scheduler.mu.Unlock()
		return View{}, errors.New("任务不存在")
	}
	task := scheduler.tasks[index]
	if state := scheduler.state[id]; state != nil && state.Running {
		scheduler.mu.Unlock()
		return View{}, errors.New("任务正在运行")
	}
	scheduler.mu.Unlock()
	state, err := scheduler.execute(ctx, task, scheduler.now())
	return View{task, state}, err
}

func (scheduler *Scheduler) due(now time.Time) []Task {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	var tasks []Task
	for _, task := range scheduler.tasks {
		state := scheduler.state[task.ID]
		var last time.Time
		if state != nil {
			if state.Running {
				continue
			}
			last = state.LastRun
		}
		if task.Due(now, last) {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func (scheduler *Scheduler) execute(ctx context.Context, task Task, now time.Time) (State, error) {
	scheduler.mu.Lock()
	state := scheduler.state[task.ID]
	if state == nil {
		state = &State{}
		scheduler.state[task.ID] = state
	}
	state.Running, state.LastRun = true, now
	scheduler.mu.Unlock()

	var card render.Card
	err := errors.New("未知的任务类型")
	if source := plugin.LookupSource(task.Type); source != nil {
		ctx, cancel := context.WithTimeout(ctx, runTimeout)
		card, err = source.Fetch(ctx, plugin.Job{Name: task.Name, Settings: maps.Clone(task.Settings)})
		cancel()
	}
	if err == nil {
		err = scheduler.show(card)
	}

	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	state.Running = false
	if err != nil {
		state.Result, state.Error = "", err.Error()
	} else {
		summary := strings.ReplaceAll(strings.TrimSpace(card.Title+" · "+card.Body), "\n", " · ")
		state.Result, state.Error = summary, ""
	}
	return *state, err
}

// Due reports whether the task should run at now, given its previous run.
// Runs are aligned to Start + k*Interval inside the active window.
func (task Task) Due(now, last time.Time) bool {
	if !task.Enabled {
		return false
	}
	start, _ := clock(task.Start)
	end, _ := clock(task.End)
	length := (end - start + 1440) % 1440
	if length == 0 {
		length = 1440
	}
	for back := 0; back <= 1; back++ {
		day := now.AddDate(0, 0, -back)
		opened := time.Date(day.Year(), day.Month(), day.Day(), start/60, start%60, 0, 0, now.Location())
		if now.Before(opened) || !now.Before(opened.Add(time.Duration(length)*time.Minute)) {
			continue
		}
		if !slices.Contains(task.Days, isoWeekday(opened)) {
			continue
		}
		step := time.Duration(task.Interval) * time.Minute
		slot := opened.Add(now.Sub(opened) / step * step)
		return last.Before(slot)
	}
	return false
}

func isoWeekday(value time.Time) int {
	if value.Weekday() == time.Sunday {
		return 7
	}
	return int(value.Weekday())
}

func clock(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, fmt.Errorf("时间格式应为 HH:MM：%q", value)
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func (task Task) normalized() (Task, error) {
	source := plugin.LookupSource(task.Type)
	if source == nil {
		return task, errors.New("未知的任务类型")
	}
	task.Name = strings.TrimSpace(task.Name)
	if utf8.RuneCountInString(task.Name) > 32 {
		return task, errors.New("名称最多 32 个字符")
	}
	if task.Name == "" {
		task.Name = source.Info().Name
	}
	days := make([]int, 0, 7)
	for _, day := range task.Days {
		if day < 1 || day > 7 {
			return task, errors.New("星期必须在 1（周一）到 7（周日）之间")
		}
		if !slices.Contains(days, day) {
			days = append(days, day)
		}
	}
	if len(days) == 0 {
		return task, errors.New("至少选择一天")
	}
	slices.Sort(days)
	task.Days = days
	for _, value := range []string{task.Start, task.End} {
		if _, err := clock(value); err != nil {
			return task, err
		}
	}
	if task.Interval < 1 || task.Interval > 1440 {
		return task, errors.New("间隔必须在 1 到 1440 分钟之间")
	}
	settings, err := plugin.Clean(source.Fields(), task.Settings)
	if err != nil {
		return task, err
	}
	settings, err = source.Normalize(settings)
	if err != nil {
		return task, err
	}
	if len(settings) == 0 {
		settings = nil
	}
	task.Settings = settings
	return task, nil
}
