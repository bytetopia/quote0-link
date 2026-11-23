package server

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/device"
	"github.com/bytetopia/quote0-link/app/internal/render"
)

type fakeSender struct {
	mu     sync.Mutex
	sends  int
	serial string
	fail   error
}

func (sender *fakeSender) Send(device.Frame) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.sends++
	return sender.fail
}

func (sender *fakeSender) Target() (device.Target, error) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return device.Target{Port: "COM9", USBSerial: sender.serial}, nil
}

func (sender *fakeSender) count() int {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.sends
}

func show(t *testing.T, service *Service, frame device.Frame, force bool) Status {
	t.Helper()
	service.mu.Lock()
	job := service.enqueue(frame, force).Job
	service.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		service.mu.Lock()
		status := service.status
		service.mu.Unlock()
		if status.Job == job && status.State != "queued" && status.State != "sending" {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job %d did not finish", job)
	return Status{}
}

func TestRunSkipsUnchangedFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "last-frame.json")
	sender := &fakeSender{serial: "A"}
	service := New("", "", "", render.Renderer{}, sender)
	service.PersistFrameCache(path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)

	var one, two device.Frame
	two[0] = 1
	steps := []struct {
		frame device.Frame
		force bool
		state string
		sends int
	}{
		{one, false, "shown", 1},
		{one, false, "unchanged", 1},
		{one, true, "shown", 2},
		{two, false, "shown", 3},
		{two, false, "unchanged", 3},
	}
	for i, step := range steps {
		if status := show(t, service, step.frame, step.force); status.State != step.state || sender.count() != step.sends {
			t.Fatalf("step %d: state %q sends %d, want %q %d", i, status.State, sender.count(), step.state, step.sends)
		}
	}

	sender.mu.Lock()
	sender.serial = "B"
	sender.mu.Unlock()
	if status := show(t, service, two, false); status.State != "shown" || sender.count() != 4 {
		t.Fatalf("other device: state %q sends %d", status.State, sender.count())
	}

	restarted := New("", "", "", render.Renderer{}, sender)
	restarted.PersistFrameCache(path)
	go restarted.Run(ctx)
	if status := show(t, restarted, two, false); status.State != "unchanged" || sender.count() != 4 {
		t.Fatalf("after restart: state %q sends %d", status.State, sender.count())
	}

	restarted.mu.Lock()
	restarted.resetDisplay = true
	restarted.mu.Unlock()
	if status := show(t, restarted, two, false); status.State != "shown" || sender.count() != 5 {
		t.Fatalf("after maintenance: state %q sends %d", status.State, sender.count())
	}

	sender.mu.Lock()
	sender.fail = errors.New("unplugged")
	sender.mu.Unlock()
	if status := show(t, restarted, two, true); status.State != "error" {
		t.Fatalf("failed send: state %q", status.State)
	}
	sender.mu.Lock()
	sender.fail = nil
	sender.mu.Unlock()
	if status := show(t, restarted, two, false); status.State != "shown" || sender.count() != 7 {
		t.Fatalf("after error: state %q sends %d", status.State, sender.count())
	}
}
