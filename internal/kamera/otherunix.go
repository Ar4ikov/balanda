//go:build unix && !linux && !darwin

package kamera

import (
	"os/exec"
	"strconv"
	"strings"
)

func psStat(pid int) string {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// На BSD время старта не сверяем.
func procStart(int) uint64 { return 0 }

func isZombie(pid int) bool { return strings.HasPrefix(psStat(pid), "Z") }

func isStopped(pid int) bool { return strings.HasPrefix(psStat(pid), "T") }
