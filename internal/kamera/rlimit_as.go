//go:build unix && !openbsd

package kamera

import "golang.org/x/sys/unix"

const rlimitAS = unix.RLIMIT_AS
