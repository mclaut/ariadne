package main

import (
	"time"

	"golang.org/x/sys/windows"
)

func readSystemBootTime() (time.Time, bool) {
	uptime := windows.DurationSinceBoot()
	if uptime <= 0 {
		return time.Time{}, false
	}
	return time.Now().Add(-uptime), true
}
