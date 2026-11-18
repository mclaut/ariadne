//go:build !darwin && !linux && !windows

package main

import "time"

func readSystemBootTime() (time.Time, bool) { return time.Time{}, false }
