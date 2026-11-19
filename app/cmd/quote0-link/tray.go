//go:build darwin || windows

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"runtime"

	"github.com/getlantern/systray"
)

//go:embed tray-template.png
var trayTemplate []byte

//go:embed tray-color.png
var trayColor []byte

func tray(ctx context.Context, canQuit func() bool, open func()) {
	systray.Run(func() {
		if runtime.GOOS == "darwin" {
			systray.SetTemplateIcon(trayTemplate, trayTemplate)
		} else {
			systray.SetIcon(windowsTrayIcon())
		}
		systray.SetTooltip("Quote0 Link")
		manage := systray.AddMenuItem("打开管理页", "Quote0 Link")
		quit := systray.AddMenuItem("退出 Quote0 Link", "停止服务")
		go open()
		go func() {
			for {
				select {
				case <-manage.ClickedCh:
					open()
				case <-quit.ClickedCh:
					if canQuit() {
						systray.Quit()
						return
					}
					open()
				case <-ctx.Done():
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}

func windowsTrayIcon() []byte {
	var ico bytes.Buffer
	ico.Write([]byte{0, 0, 1, 0, 1, 0, 32, 32, 0, 0, 1, 0, 32, 0})
	_ = binary.Write(&ico, binary.LittleEndian, uint32(len(trayColor)))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(22))
	ico.Write(trayColor)
	return ico.Bytes()
}
