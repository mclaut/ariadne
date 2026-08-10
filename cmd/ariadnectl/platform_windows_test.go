//go:build windows

package main

import "testing"

func TestControlReturnsServiceCommandFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := control("start"); err == nil {
		t.Fatal("control reported success when schtasks.exe was unavailable")
	}
}
