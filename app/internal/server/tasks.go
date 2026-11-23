package server

import (
	"errors"
	"net/http"

	"github.com/bytetopia/quote0-link/app/internal/render"
	"github.com/bytetopia/quote0-link/app/internal/schedule"
)

var errMaintenance = errors.New("设备维护中，已暂停发送")

// EnableTasks exposes the scheduler on /v1/tasks. tokenFile is shown on the
// API page so external tools know where to read the bearer token.
func (service *Service) EnableTasks(scheduler *schedule.Scheduler, tokenFile string) {
	service.tasks, service.tokenFile = scheduler, tokenFile
}

// Show renders a card from an active integration and queues it for the
// display. Frames identical to the panel content are skipped by Run.
func (service *Service) Show(card render.Card) error {
	frame, err := service.renderer.Draw(card)
	if err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.maintenanceBusy() {
		return errMaintenance
	}
	service.enqueue(frame, false)
	return nil
}

func (service *Service) listTasks(writer http.ResponseWriter, _ *http.Request) {
	respond(writer, http.StatusOK, map[string]any{"tasks": service.tasks.List(), "kinds": schedule.Kinds()})
}

func (service *Service) saveTask(writer http.ResponseWriter, request *http.Request) {
	var task schedule.Task
	if err := decode(writer, request, 8192, &task); err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	task.ID = request.PathValue("id")
	saved, err := service.tasks.Save(task)
	if err != nil {
		fail(writer, http.StatusBadRequest, err)
		return
	}
	status := http.StatusOK
	if task.ID == "" {
		status = http.StatusCreated
	}
	respond(writer, status, saved)
}

func (service *Service) deleteTask(writer http.ResponseWriter, request *http.Request) {
	if err := service.tasks.Delete(request.PathValue("id")); err != nil {
		fail(writer, http.StatusNotFound, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (service *Service) runTask(writer http.ResponseWriter, request *http.Request) {
	view, err := service.tasks.RunNow(request.Context(), request.PathValue("id"))
	if err != nil && view.ID == "" {
		fail(writer, http.StatusNotFound, err)
		return
	}
	respond(writer, http.StatusOK, view)
}

type endpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Body        string `json:"body,omitempty"`
}

func (service *Service) apiInfo(writer http.ResponseWriter, _ *http.Request) {
	base := "http://" + service.host
	tokenFile := service.tokenFile
	if tokenFile == "" {
		tokenFile = "api-token"
	}
	body := `{"template":"card","title":"构建完成","subtitle":"CI","body":"全部测试通过"}`
	respond(writer, http.StatusOK, map[string]any{
		"base_url":   base,
		"token_file": tokenFile,
		"auth":       "Authorization: Bearer <token>（每次启动生成新 token，写入 token 文件）",
		"endpoints": []endpoint{
			{"POST", "/v1/display", "显示一张卡片；template 可为 card、text 或 image（image 用 image_base64 传 PNG/JPEG）；与屏幕当前内容相同时跳过，加 ?force=true 强制刷新", body},
			{"POST", "/v1/events", "上报 Agent 状态，由优先级最高的会话占据屏幕", `{"source":"codex","session":"abc","state":"working|approval|finished|closed"}`},
			{"GET", "/v1/status", "最近一次发送的状态", ""},
			{"GET", "/v1/device", "设备连接与固件信息", ""},
			{"GET", "/v1/tasks", "定时任务列表与可用任务类型", ""},
			{"POST", "/v1/tasks/{id}/run", "立即执行一个定时任务", ""},
		},
		"examples": map[string]string{
			"powershell": "$token = (Get-Content \"" + tokenFile + "\").Trim()\n" +
				"Invoke-RestMethod -Method Post -Uri " + base + "/v1/display `\n" +
				"  -Headers @{Authorization = \"Bearer $token\"} -ContentType 'application/json; charset=utf-8' `\n" +
				"  -Body ([Text.Encoding]::UTF8.GetBytes('" + body + "'))",
			"curl": "curl -X POST " + base + "/v1/display \\\n" +
				"  -H \"Authorization: Bearer $(cat '" + tokenFile + "')\" \\\n" +
				"  -H 'Content-Type: application/json' \\\n" +
				"  -d '" + body + "'",
		},
	})
}
