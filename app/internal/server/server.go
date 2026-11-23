package server

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/device"
	"github.com/bytetopia/quote0-link/app/internal/maintenance"
	"github.com/bytetopia/quote0-link/app/internal/plugin"
	"github.com/bytetopia/quote0-link/app/internal/render"
	"github.com/bytetopia/quote0-link/app/internal/schedule"
)

//go:embed web/*
var assets embed.FS

type FrameSender interface {
	Send(device.Frame) error
}

type Status struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	Job   uint64 `json:"job"`
}

type job struct {
	frame device.Frame
	id    uint64
	force bool
}

type Service struct {
	host, token, executable string
	renderer                render.Renderer
	sender                  FrameSender
	mu                      sync.Mutex
	configMu                sync.Mutex
	sessions                plugin.Sessions
	tasks                   *schedule.Scheduler
	tokenFile               string
	wake                    chan struct{}
	pending                 *job
	status                  Status
	maint                   *maintenance.Manager
	maintenanceToken        string
	resetDisplay            bool
	cache                   *frameCache
}

func (service *Service) EnableMaintenance(manager *maintenance.Manager, token string) {
	service.maint, service.maintenanceToken = manager, token
}

func New(host, token, executable string, renderer render.Renderer, sender FrameSender) *Service {
	return &Service{
		host: host, token: token, executable: executable, renderer: renderer, sender: sender,
		status: Status{State: "idle"}, cache: loadFrameCache(""), sessions: make(plugin.Sessions), wake: make(chan struct{}, 1),
	}
}

func (service *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", service.asset("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /ui.js", service.asset("ui.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /ui.css", service.asset("ui.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /favicon.svg", service.asset("favicon.svg", "image/svg+xml"))
	mux.HandleFunc("GET /v1/device", service.auth(service.getDevice))
	mux.HandleFunc("GET /v1/status", service.auth(service.getStatus))
	mux.HandleFunc("POST /v1/display", service.auth(service.display))
	mux.HandleFunc("POST /v1/events", service.auth(service.event))
	mux.HandleFunc("GET /v1/integrations", service.auth(service.catalog))
	mux.HandleFunc("PUT /v1/integrations/{id}", service.auth(service.configure))
	mux.HandleFunc("GET /v1/api", service.auth(service.apiInfo))
	if service.tasks != nil {
		mux.HandleFunc("GET /v1/tasks", service.auth(service.listTasks))
		mux.HandleFunc("POST /v1/tasks", service.auth(service.saveTask))
		mux.HandleFunc("PUT /v1/tasks/{id}", service.auth(service.saveTask))
		mux.HandleFunc("DELETE /v1/tasks/{id}", service.auth(service.deleteTask))
		mux.HandleFunc("POST /v1/tasks/{id}/run", service.auth(service.runTask))
	}
	if service.maint != nil {
		mux.HandleFunc("GET /v1/maintenance", service.maintenanceAuth(service.maintenanceStatus))
		mux.HandleFunc("POST /v1/maintenance", service.maintenanceAuth(service.startMaintenance))
		mux.HandleFunc("GET /v1/maintenance/backup/{id}", service.maintenanceAuth(service.downloadBackup))
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != service.host {
			http.Error(writer, "invalid Host", http.StatusForbidden)
			return
		}
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(writer, request)
	})
}

func (service *Service) asset(name, mediaType string) http.HandlerFunc {
	body, _ := assets.ReadFile("web/" + name)
	return func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", mediaType)
		_, _ = writer.Write(body)
	}
}

func (service *Service) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), []byte("Bearer "+service.token)) != 1 {
			fail(writer, http.StatusUnauthorized, errors.New("invalid local API token"))
			return
		}
		next(writer, request)
	}
}

func (service *Service) getStatus(writer http.ResponseWriter, _ *http.Request) {
	service.mu.Lock()
	status := service.status
	service.mu.Unlock()
	respond(writer, http.StatusOK, status)
}

func (service *Service) getDevice(writer http.ResponseWriter, _ *http.Request) {
	var status device.Status
	if service.maintenanceBusy() {
		status.State = "maintenance"
	} else {
		locator, ok := service.sender.(interface{ Status() device.Status })
		if !ok {
			fail(writer, http.StatusNotImplemented, errors.New("serial detection unavailable"))
			return
		}
		status = locator.Status()
	}
	var bundled string
	if service.maint != nil {
		bundled = service.maint.FirmwareVersion()
	}
	respond(writer, http.StatusOK, struct {
		device.Status
		BundledVersion  string `json:"bundled_firmware_version,omitempty"`
		UpdateAvailable bool   `json:"update_available,omitempty"`
	}{status, bundled, status.CanUpdateTo(bundled)})
}

// PersistFrameCache stores the last-shown frame fingerprint at path so the
// skip-unchanged check also survives restarts. Call before Run.
func (service *Service) PersistFrameCache(path string) {
	service.cache = loadFrameCache(path)
}

// deviceID identifies the connected panel so a cached frame is never trusted
// for a different device. Windows often reports no serial for the composite
// device's CDC interface, so the port name is the fallback.
func (service *Service) deviceID() string {
	if locator, ok := service.sender.(interface{ Target() (device.Target, error) }); ok {
		if target, err := locator.Target(); err == nil {
			if target.USBSerial != "" {
				return target.USBSerial
			}
			return target.Port
		}
	}
	return ""
}

// enqueue queues frame for the panel. Unless force is set, a frame identical
// to what the panel already shows is skipped.
func (service *Service) enqueue(frame device.Frame, force bool) Status {
	service.status = Status{State: "queued", Job: service.status.Job + 1}
	service.pending = &job{frame, service.status.Job, force}
	select {
	case service.wake <- struct{}{}:
	default:
	}
	return service.status
}

func (service *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-service.wake:
		}
		service.mu.Lock()
		current := service.pending
		service.pending = nil
		if service.resetDisplay {
			service.cache.reset()
			service.resetDisplay = false
		}
		if current != nil {
			service.status.State = "sending"
		}
		service.mu.Unlock()
		if current == nil {
			continue
		}
		var err error
		id := service.deviceID()
		skipped := !current.force && service.cache.same(id, &current.frame)
		if !skipped {
			if err = service.sender.Send(current.frame); err == nil {
				service.cache.remember(id, &current.frame)
			} else {
				service.cache.reset()
			}
		}
		service.mu.Lock()
		if service.status.Job == current.id {
			service.status.State = "shown"
			if skipped {
				service.status.State = "unchanged"
			} else if err != nil {
				service.status.State, service.status.Error = "error", err.Error()
			}
		}
		service.mu.Unlock()
	}
}

func (service *Service) display(writer http.ResponseWriter, request *http.Request) {
	var card render.Card
	if err := decode(writer, request, 6<<20, &card); err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	frame, err := service.renderer.Draw(card)
	if err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	service.mu.Lock()
	if service.maintenanceBusy() {
		service.mu.Unlock()
		fail(writer, http.StatusConflict, errors.New("设备维护中，已暂停发送"))
		return
	}
	force, _ := strconv.ParseBool(request.URL.Query().Get("force"))
	status := service.enqueue(frame, force)
	service.mu.Unlock()
	respond(writer, http.StatusAccepted, status)
}

func (service *Service) event(writer http.ResponseWriter, request *http.Request) {
	var event plugin.Event
	if err := decode(writer, request, 4096, &event); err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	if !event.Valid() {
		fail(writer, http.StatusBadRequest, errors.New("invalid source, session or state"))
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.maintenanceBusy() {
		fail(writer, http.StatusConflict, errors.New("设备维护中，已暂停发送"))
		return
	}
	selected := service.sessions.Apply(event, time.Now())
	card := render.Card{Template: "card", Title: "Quote0 Link", Subtitle: "INTEGRATIONS", Body: "Ready"}
	if hook := plugin.LookupHook(selected.Source); hook != nil {
		card.Title = hook.Info().Name
		card.Body = map[string]string{plugin.StateWorking: "Working", plugin.StateApproval: "Waiting for approval", plugin.StateFinished: "Turn finished"}[selected.State]
	}
	frame, err := service.renderer.Draw(card)
	if err != nil {
		fail(writer, http.StatusInternalServerError, err)
		return
	}
	respond(writer, http.StatusAccepted, service.enqueue(frame, false))
}

func (service *Service) catalog(writer http.ResponseWriter, _ *http.Request) {
	catalog := []plugin.Info{}
	for _, hook := range plugin.Hooks() {
		catalog = append(catalog, hook.Info())
	}
	respond(writer, http.StatusOK, catalog)
}

func (service *Service) configure(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(writer, request, 1024, &input); err != nil || input.Enabled == nil {
		fail(writer, http.StatusBadRequest, errors.New("expected an enabled boolean"))
		return
	}
	hook := plugin.LookupHook(request.PathValue("id"))
	if hook == nil {
		fail(writer, http.StatusBadRequest, errors.New("unknown integration"))
		return
	}
	service.configMu.Lock()
	defer service.configMu.Unlock()
	path, err := hook.Configure(service.executable, *input.Enabled)
	if err != nil {
		fail(writer, http.StatusInternalServerError, err)
		return
	}
	respond(writer, http.StatusOK, map[string]any{"enabled": *input.Enabled, "path": path})
}

func decode(writer http.ResponseWriter, request *http.Request, max int64, target any) error {
	if !strings.EqualFold(strings.TrimSpace(strings.SplitN(request.Header.Get("Content-Type"), ";", 2)[0]), "application/json") {
		return errors.New("expected application/json")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, max))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func respond(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func fail(writer http.ResponseWriter, status int, err error) {
	respond(writer, status, map[string]string{"error": err.Error()})
}
