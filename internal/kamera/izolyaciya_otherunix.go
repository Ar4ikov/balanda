//go:build unix && !linux && !darwin

package kamera

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const needOwnTmp = false

var errNoIzolyaciya = errors.New("изоляция есть только на Linux, macOS и Windows; запустите с --bez-izolyacii")

func isolateCmd(*exec.Cmd, Izolyaciya) error { return errNoIzolyaciya }

// ProbeIzolyaciya на BSD всегда отказывает.
func ProbeIzolyaciya() error { return errNoIzolyaciya }

func zekRun(o zekOpts) int {
	if o.odinochka {
		fmt.Fprintln(os.Stderr, "balanda:", errNoIzolyaciya)
		return ExitNoStart
	}
	if err := applyRlimits(o); err != nil {
		fmt.Fprintln(os.Stderr, "balanda:", err)
		return ExitNoStart
	}
	err := syscall.Exec(o.path, o.argv, os.Environ())
	fmt.Fprintf(os.Stderr, "balanda: не запустить %s: %v\n", o.path, err)
	return ExitNoStart
}
