package main

import (
	"time"

	"golang.org/x/sys/unix"
)

func readSystemBootTime() (time.Time, bool) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil || info.Uptime <= 0 {
		return time.Time{}, false
	}
	return time.Now().Add(-time.Duration(info.Uptime) * time.Second), true
}
