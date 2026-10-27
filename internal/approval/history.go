package approval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Entry preserves the request alongside its current authorization lifecycle.
type Entry struct {
	Request Request `json:"request"`
	Status  Status  `json:"status"`
}

// History includes decided and expired requests without modifying any records.
// Invalid records produce an explicit error instead of silently disappearing.
func (m *Manager) History() ([]Entry, error) {
	files, err := os.ReadDir(m.dir("requests"))
	if os.IsNotExist(err) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(files))
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		id := file.Name()[:len(file.Name())-5]
		request, err := m.loadRequest(id)
		if err != nil {
			return nil, fmt.Errorf("read request %s: %w", id, err)
		}
		status, err := m.Status(id)
		if err != nil {
			return nil, fmt.Errorf("read status %s: %w", id, err)
		}
		entries = append(entries, Entry{Request: request, Status: status})
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Status.State == "pending") != (entries[j].Status.State == "pending") {
			return entries[i].Status.State == "pending"
		}
		return entries[i].Request.CreatedAt.After(entries[j].Request.CreatedAt)
	})
	return entries, nil
}
