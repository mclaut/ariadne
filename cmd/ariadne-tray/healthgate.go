package main

import "slices"

// healthGate keeps one-poll blips (a busy metrics read, a collection briefly
// yellow while Qdrant optimizes) from flipping the dot or sending a
// notification. A new color is shown only after two consecutive polls agree;
// the first poll is shown at once so the tray never starts blank.
type healthGate struct {
	shown, last string
	lastIssues  []string
	notified    []string
}

// observe returns the color to show, whether it changed, and the issues to
// notify about (nil when nothing new has persisted).
func (g *healthGate) observe(color string, issues []string, reachable bool) (string, bool, []string) {
	previous := g.shown
	if g.shown == "" || color == g.last {
		g.shown = color
	}
	g.last = color
	persisted := slices.Equal(issues, g.lastIssues)
	g.lastIssues = issues
	changed := g.shown != previous

	if g.shown == healthGreen || len(issues) == 0 {
		g.notified = nil
		return g.shown, changed, nil
	}
	// Notify when a worse color has just settled after two agreeing polls, or
	// when a different set of issues has held for two polls while the dot is
	// not green. The tray's first poll alone never notifies.
	settled := changed && previous != ""
	if reachable && g.shown == color && (settled || persisted) && !slices.Equal(issues, g.notified) {
		g.notified = issues
		return g.shown, changed, issues
	}
	return g.shown, changed, nil
}
