package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

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
	input, backup, err := readMaintenanceRequest(writer, request)
	if err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.maint.Start(input, backup); err != nil {
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

func readMaintenanceRequest(writer http.ResponseWriter, request *http.Request) (maintenance.Request, []byte, error) {
	var input maintenance.Request
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		err := decode(writer, request, 2048, &input)
		return input, nil, err
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maintenance.MaxBackup+(16<<10))
	parts, err := request.MultipartReader()
	if err != nil {
		return input, nil, err
	}
	details, err := parts.NextPart()
	if err != nil || details.FormName() != "request" || details.FileName() != "" {
		return input, nil, errors.New("缺少操作参数")
	}
	const detailsLimit = 2048
	metadata, err := io.ReadAll(io.LimitReader(details, detailsLimit+1))
	if err != nil || len(metadata) > detailsLimit {
		return input, nil, errors.New("操作参数无效")
	}
	decoder := json.NewDecoder(strings.NewReader(string(metadata)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF {
		return input, nil, errors.New("操作参数无效")
	}
	file, err := parts.NextPart()
	if err != nil || file.FormName() != "backup" || !strings.HasSuffix(strings.ToLower(file.FileName()), ".q0backup") {
		return input, nil, errors.New("请选择一个 .q0backup 文件")
	}
	data, err := io.ReadAll(io.LimitReader(file, maintenance.MaxBackup+1))
	if err != nil || len(data) > maintenance.MaxBackup {
		return input, nil, errors.New("无法读取备份，或文件过大")
	}
	if _, err := parts.NextPart(); err != io.EOF {
		return input, nil, errors.New("只允许提交一个备份文件")
	}
	return input, data, nil
}

func (service *Service) downloadBackup(writer http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseUint(request.PathValue("id"), 10, 64)
	if err != nil {
		fail(writer, http.StatusNotFound, errors.New("无效的下载编号"))
		return
	}
	info, data, err := service.maint.Download(id)
	if err != nil {
		fail(writer, http.StatusNotFound, err)
		return
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Content-Disposition", `attachment; filename="`+info.Filename+`"`)
	_, _ = writer.Write(data)
}
