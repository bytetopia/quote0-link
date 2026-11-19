package server

import (
	"crypto/subtle"
	"errors"
	"io"
	"net/http"

	"github.com/bytetopia/quote0-link/app/internal/maintenance"
)

func (service *Service) maintenanceBusy() bool {
	return service.maint != nil && service.maint.Busy()
}

func (service *Service) maintenanceAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if service.maintenanceToken == "" ||
			subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), []byte("Bearer "+service.maintenanceToken)) != 1 {
			fail(writer, http.StatusUnauthorized, errors.New("请从系统托盘打开管理页进行设备维护"))
			return
		}
		next(writer, request)
	}
}

func (service *Service) maintenanceStatus(writer http.ResponseWriter, _ *http.Request) {
	respond(writer, http.StatusOK, service.maint.Status())
}

func (service *Service) startMaintenance(writer http.ResponseWriter, request *http.Request) {
	var input maintenance.Request
	if err := decode(writer, request, 2048, &input); err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.maint.Start(input); err != nil {
		fail(writer, http.StatusConflict, err)
		return
	}
	service.resetDisplay = true
	if service.pending != nil {
		service.pending = nil
		service.status.State = "cancelled"
	}
	respond(writer, http.StatusAccepted, service.maint.Status())
}

func (service *Service) importBackup(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Content-Type") != "application/octet-stream" {
		fail(writer, http.StatusUnsupportedMediaType, errors.New("需要 .q0backup 文件"))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maintenance.MaxBackup))
	if err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	mac, err := service.maint.Import(data)
	if err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	respond(writer, http.StatusCreated, map[string]string{"mac": mac})
}

func (service *Service) exportBackup(writer http.ResponseWriter, request *http.Request) {
	mac := request.PathValue("mac")
	data, err := service.maint.Export(mac)
	if err != nil {
		fail(writer, http.StatusNotFound, err)
		return
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Content-Disposition", `attachment; filename="quote0-`+mac+`.q0backup"`)
	_, _ = writer.Write(data)
}
