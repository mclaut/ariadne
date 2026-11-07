package main

import (
	"ariadne/internal/activity"
	"ariadne/internal/i18n"
	"fmt"
	"time"
)

const (
	maintenanceStatusFailed      = "failed"
	maintenanceStatusPartial     = "partial"
	maintenanceStatusDeferred    = "deferred"
	defaultMaintenanceStaleAfter = 36 * time.Hour
	defaultMaintenanceStuckAfter = 2 * time.Hour
)

func maintenanceHealthIssue(now time.Time, events map[string]activity.Event, lang i18n.Lang) string {
	maintenance, hasMaintenance := events["maintenance"]
	if hasMaintenance && maintenance.Status == "retrying" {
		if now.Sub(maintenance.At) > durationEnv("ARIADNE_MAINTENANCE_STUCK_AFTER", defaultMaintenanceStuckAfter) {
			return fmt.Sprintf(i18n.T(lang, "issue.maintenance_stuck"), maintenanceEventTime(maintenance))
		}
		return i18n.T(lang, "issue.maintenance_retrying")
	}
	for _, operation := range []string{"maintenance", "memfile_sync", "consolidate"} {
		child, ok := events[operation]
		if !ok || (child.Status != maintenanceStatusFailed && child.Status != maintenanceStatusPartial &&
			child.Status != maintenanceStatusDeferred) {
			continue
		}
		if hasMaintenance && (maintenance.Status == backupStatusComplete || maintenance.Status == "complete_with_deferred") &&
			!maintenance.At.Before(child.At) {
			continue
		}
		return fmt.Sprintf(i18n.T(lang, "issue.maintenance_failed"), operation+": "+child.Status)
	}
	event, ok := latestMaintenanceEvent(events)
	if !ok {
		return i18n.T(lang, "issue.maintenance_missing")
	}
	age := now.Sub(event.At)
	if age < 0 {
		age = 0
	}
	switch event.Status {
	case maintenanceStatusFailed:
		return fmt.Sprintf(i18n.T(lang, "issue.maintenance_failed"), event.Status)
	case maintenanceStatusPartial, maintenanceStatusDeferred:
		return fmt.Sprintf(i18n.T(lang, "issue.maintenance_degraded"), event.Status)
	case "running", "retrying":
		if age > durationEnv("ARIADNE_MAINTENANCE_STUCK_AFTER", defaultMaintenanceStuckAfter) {
			return fmt.Sprintf(i18n.T(lang, "issue.maintenance_stuck"), maintenanceEventTime(event))
		}
		return ""
	default:
		if age > durationEnv("ARIADNE_MAINTENANCE_STALE_AFTER", defaultMaintenanceStaleAfter) {
			return fmt.Sprintf(i18n.T(lang, "issue.maintenance_stale"), maintenanceEventTime(event))
		}
		return ""
	}
}

func backupHealthIssue(events map[string]activity.Event, lang i18n.Lang) string {
	event, ok := events["backup"]
	if !ok || event.Status == backupStatusComplete {
		return ""
	}
	return fmt.Sprintf(i18n.T(lang, "issue.backup_failed"), event.Status)
}

func latestMaintenanceEvent(events map[string]activity.Event) (activity.Event, bool) {
	var latest activity.Event
	found := false
	for _, operation := range []string{"maintenance", "memfile_sync", "consolidate"} {
		event, ok := events[operation]
		if !ok || event.At.IsZero() {
			continue
		}
		if !found || event.At.After(latest.At) {
			latest = event
			found = true
		}
	}
	return latest, found
}

func maintenanceEventTime(event activity.Event) string {
	return event.At.Local().Format("2006-01-02 15:04")
}
