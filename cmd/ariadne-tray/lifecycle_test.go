package main

import "testing"

func TestSupervisedTrayDetection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, platform, service string
		want                    bool
	}{
		{name: "canonical macOS launchd job", platform: osDarwin, service: "com.ariadne.tray", want: true},
		{name: "versioned macOS launchd job", platform: osDarwin, service: "com.ariadne.tray.v0-8-5", want: true},
		{name: "unrelated macOS job", platform: osDarwin, service: "com.example.tray", want: false},
		{name: "manual macOS process", platform: osDarwin, service: "", want: false},
		{name: "Linux autostart", platform: osLinux, service: "com.ariadne.tray", want: false},
		{name: "Windows startup", platform: osWindows, service: "com.ariadne.tray", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := supervisedTray(tc.platform, tc.service); got != tc.want {
				t.Fatalf("supervisedTray(%q, %q) = %v, want %v", tc.platform, tc.service, got, tc.want)
			}
		})
	}
}
