package server

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/device"
	"github.com/bytetopia/quote0-link/app/internal/integration"
	"github.com/bytetopia/quote0-link/app/internal/maintenance"
	"github.com/bytetopia/quote0-link/app/internal/render"
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
	frame  device.Frame
	id     uint64
	dedupe bool
}

type Service struct {
	host, token, executable string
	renderer                render.Renderer
	sender                  FrameSender
	mu                      sync.Mutex
	configMu                sync.Mutex
	sessions                integration.Sessions
	wake                    chan struct{}
	pending                 *job
	status                  Status
	maint                   *maintenance.Manager
	maintenanceToken        string
	resetDisplay            bool
}

func (service *Service) EnableMaintenance(manager *maintenance.Manager, token string) {
	service.maint, service.maintenanceToken = manager, token
}

func New(host, token, executable string, renderer render.Renderer, sender FrameSender) *Service {
	return &Service{
		host: host, token: token, executable: executable, renderer: renderer, sender: sender,
		status: Status{State: "idle"}, sessions: make(integration.Sessions), wake: make(chan struct{}, 1),
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

func (service *Service) enqueue(frame device.Frame, dedupe bool) Status {
	service.status = Status{State: "queued", Job: service.status.Job + 1}
	service.pending = &job{frame, service.status.Job, dedupe}
	select {
	case service.wake <- struct{}{}:
	default:
	}
	return service.status
}

func (service *Service) Run(ctx context.Context) {
	var last *device.Frame
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
			last = nil
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
		if last == nil || !current.dedupe || *last != current.frame {
			err = service.sender.Send(current.frame)
		}
		if err == nil {
			last = &current.frame
		} else {
			last = nil
		}
		service.mu.Lock()
		if service.status.Job == current.id {
			service.status.State = "shown"
			if err != nil {
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
	status := service.enqueue(frame, false)
	service.mu.Unlock()
	respond(writer, http.StatusAccepted, status)
}

func (service *Service) event(writer http.ResponseWriter, request *http.Request) {
	var event integration.Event
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
	for _, adapter := range integration.Catalog {
		if selected.Source == adapter.ID {
			card.Title = adapter.Name
			card.Body = map[string]string{"working": "Working", "approval": "Waiting for approval", "finished": "Turn finished"}[selected.State]
			break
		}
	}
	frame, err := service.renderer.Draw(card)
	if err != nil {
		fail(writer, http.StatusInternalServerError, err)
		return
	}
	respond(writer, http.StatusAccepted, service.enqueue(frame, true))
}

func (service *Service) catalog(writer http.ResponseWriter, _ *http.Request) {
	respond(writer, http.StatusOK, integration.Catalog)
}

func (service *Service) configure(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(writer, request, 1024, &input); err != nil || input.Enabled == nil {
		fail(writer, http.StatusBadRequest, errors.New("expected an enabled boolean"))
		return
	}
	source := request.PathValue("id")
	path, err := integration.Path(source)
	if err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	service.configMu.Lock()
	defer service.configMu.Unlock()
	if err := integration.Configure(path, source, service.executable, *input.Enabled); err != nil {
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
