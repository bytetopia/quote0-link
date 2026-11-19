//go:build !darwin && !windows

package main

import "context"

func tray(ctx context.Context, _ func() bool, open func()) {
	open()
	<-ctx.Done()
}
