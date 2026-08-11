package main

import (
	"ariadne/internal/i18n"
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

const (
	serviceNotificationPending   = "pending"
	serviceNotificationDelivered = "delivered"
)

type serviceNotificationEvent struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Status    string    `json:"status"`
	Lang      i18n.Lang `json:"lang,omitempty"`
	QdrantPID int       `json:"qdrant_pid,omitempty"`
	OllamaPID int       `json:"ollama_pid,omitempty"`
}

func queueRestartNotification(activeLang i18n.Lang, after status) error {
	event := serviceNotificationEvent{
		ID:        fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid()),
		At:        time.Now().UTC(),
		Status:    serviceNotificationPending,
		Lang:      activeLang,
		QdrantPID: after.Qdrant.PID,
		OllamaPID: after.Ollama.PID,
	}
	return appendServiceNotification(serviceNotificationPath(), event)
}

func reportPendingServiceNotification() {
	path := serviceNotificationPath()
	event, ok, err := latestPendingServiceNotification(path)
	if err != nil {
		log.Printf("read pending service notification: %v", err)
		return
	}
	if !ok {
		return
	}
	title := i18n.T(event.Lang, "notify.services")
	message := serviceActionLabel(event.Lang, serviceRestart) + ": " + i18n.T(event.Lang, "notify.done") + " · " +
		i18n.T(event.Lang, "notify.verified") + " · " + serviceStatusSummary(event.Lang, status{
		Qdrant: svc{Up: true, PID: event.QdrantPID},
		Ollama: svc{Up: true, PID: event.OllamaPID},
	})
	if err := notifySync(title, message); err != nil {
		log.Printf("deliver service notification: id=%s error=%q", event.ID, err)
		return
	}
	if err := appendServiceNotification(path, serviceNotificationEvent{
		ID: event.ID, At: time.Now().UTC(), Status: serviceNotificationDelivered,
	}); err != nil {
		log.Printf("record delivered service notification: id=%s error=%q", event.ID, err)
		return
	}
	log.Printf("service notification delivered: id=%s", event.ID)
}

func serviceNotificationPath() string {
	return runtimeDir("state/service-notifications.jsonl")
}

func appendServiceNotification(path string, event serviceNotificationEvent) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // user-owned runtime state
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // fixed runtime path
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func latestPendingServiceNotification(path string) (serviceNotificationEvent, bool, error) {
	file, err := os.Open(path) //nolint:gosec // fixed runtime path or test-owned path
	if os.IsNotExist(err) {
		return serviceNotificationEvent{}, false, nil
	}
	if err != nil {
		return serviceNotificationEvent{}, false, err
	}
	defer func() { _ = file.Close() }()

	delivered := make(map[string]bool)
	var latest serviceNotificationEvent
	hasLatest := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event serviceNotificationEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.ID == "" {
			continue
		}
		switch event.Status {
		case serviceNotificationPending:
			latest = event
			hasLatest = true
		case serviceNotificationDelivered:
			delivered[event.ID] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return serviceNotificationEvent{}, false, err
	}
	if hasLatest && !delivered[latest.ID] {
		return latest, true, nil
	}
	return serviceNotificationEvent{}, false, nil
}
