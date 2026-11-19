//go:build darwin || windows

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"runtime"

	"github.com/getlantern/systray"
)

func tray(ctx context.Context, canQuit func() bool, open func()) {
	systray.Run(func() {
		if runtime.GOOS == "darwin" {
			systray.SetTemplateIcon(trayIcon(false), trayIcon(false))
		} else {
			systray.SetIcon(trayIcon(true))
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

func trayIcon(windows bool) []byte {
	canvas := image.NewRGBA(image.Rect(0, 0, 32, 32))
	ink, paper := image.Black, image.Transparent
	if windows {
		ink = image.NewUniform(color.RGBA{36, 73, 52, 255})
		paper = image.White
	}
	draw.Draw(canvas, image.Rect(2, 4, 30, 28), ink, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(5, 7, 27, 25), paper, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(8, 11, 24, 14), ink, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(8, 18, 19, 21), ink, image.Point{}, draw.Src)
	var asset bytes.Buffer
	_ = png.Encode(&asset, canvas)
	if !windows {
		return asset.Bytes()
	}
	var ico bytes.Buffer
	ico.Write([]byte{0, 0, 1, 0, 1, 0, 32, 32, 0, 0, 1, 0, 32, 0})
	_ = binary.Write(&ico, binary.LittleEndian, uint32(asset.Len()))
	_ = binary.Write(&ico, binary.LittleEndian, uint32(22))
	ico.Write(asset.Bytes())
	return ico.Bytes()
}
