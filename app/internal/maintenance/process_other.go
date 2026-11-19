//go:build !windows

package maintenance

import "os/exec"

func configureProcess(*exec.Cmd) {}
