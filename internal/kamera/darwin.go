//go:build darwin

package kamera

import "golang.org/x/sys/unix"

// Состояния процесса из <sys/proc.h>.
const (
	darwinSSTOP = 4
	darwinSZOMB = 5
)

func kinfo(pid int) *unix.KinfoProc {
	if pid <= 0 {
		return nil
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil || int(kp.Proc.P_pid) != pid {
		return nil
	}
	return kp
}

// procStart — время старта процесса в микросекундах.
func procStart(pid int) uint64 {
	kp := kinfo(pid)
	if kp == nil {
		return 0
	}
	t := kp.Proc.P_starttime
	return uint64(t.Sec)*1_000_000 + uint64(t.Usec)
}

func isZombie(pid int) bool {
	kp := kinfo(pid)
	return kp != nil && kp.Proc.P_stat == darwinSZOMB
}

func isStopped(pid int) bool {
	kp := kinfo(pid)
	return kp != nil && kp.Proc.P_stat == darwinSSTOP
}
