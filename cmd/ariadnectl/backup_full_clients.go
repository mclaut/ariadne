package main

import (
	"ariadne/internal/backupretention"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Shared client files are parsed locally; only Ariadne-owned entries are saved.
// Other MCP servers, projects and hooks never enter the full backup.
func captureFullClients(stage string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	result := map[string]any{}
	codex := backupretention.ClientConfigPath(home, "codex-mcp")
	if b, readErr := os.ReadFile(codex); readErr == nil { //nolint:gosec // exact client config selected by its existing profile policy
		var all map[string]any
		if err = toml.Unmarshal(b, &all); err != nil {
			return errors.New("cannot parse Codex config for scoped full backup")
		}
		if servers, ok := all["mcp_servers"].(map[string]any); ok {
			if own, exists := servers["ariadne"]; exists {
				result["codex"] = map[string]any{"mcp_servers": map[string]any{"ariadne": own}}
			}
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	clients := []struct{ name, path string }{
		{"claude", filepath.Join(home, ".claude.json")},
		{"claude_settings", filepath.Join(home, ".claude", "settings.json")},
	}
	for _, item := range clients {
		b, readErr := os.ReadFile(item.path) //nolint:gosec // exact client config, only owned entries leave this function
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		var all map[string]any
		if err = json.Unmarshal(b, &all); err != nil {
			return errors.New("cannot parse Claude config for scoped full backup")
		}
		if item.name == "claude" {
			if servers, ok := all["mcpServers"].(map[string]any); ok {
				if own, exists := servers["ariadne"]; exists {
					result[item.name] = map[string]any{"mcpServers": map[string]any{"ariadne": own}}
				}
			}
			continue
		}
		hooks, _ := all["hooks"].(map[string]any)
		owned := map[string]any{}
		for event, value := range hooks {
			groups, _ := value.([]any)
			var kept []any
			for _, value := range groups {
				group, ok := value.(map[string]any)
				if !ok {
					continue
				}
				handlers, _ := group["hooks"].([]any)
				var own []any
				for _, handler := range handlers {
					h, ok := handler.(map[string]any)
					if !ok {
						continue
					}
					command, _ := h["command"].(string)
					if strings.Contains(command, "ariadne-hook") {
						own = append(own, h)
					}
				}
				if len(own) > 0 {
					copyGroup := map[string]any{"hooks": own}
					if matcher, exists := group["matcher"]; exists {
						copyGroup["matcher"] = matcher
					}
					kept = append(kept, copyGroup)
				}
			}
			if len(kept) > 0 {
				owned[event] = kept
			}
		}
		result[item.name] = map[string]any{"hooks": owned}
	}
	dir := filepath.Join(stage, "client")
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeFullJSON(filepath.Join(dir, "ariadne-sections.json"), result)
}
