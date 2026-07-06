// Package i18n localizes Ariadne's user-facing interface (tray + ariadnectl).
// The active language persists in ~/.ariadne/lang — the tray's switcher writes
// it, and every binary reads it, so the whole UI stays in one language. Adding a
// language is just adding its column to the table below (then wiring it into
// Available/Name); nothing else changes.
package i18n

import (
	"os"
	"path/filepath"
	"strings"
)

type Lang string

const (
	EN Lang = "en"
	UK Lang = "uk"
)

// Available is the switch order shown in the UI; Name holds the menu endonyms.
var (
	Available = []Lang{EN, UK}
	Name      = map[Lang]string{EN: "English", UK: "Українська"}
)

// table[key][lang] — every user-facing string. %d/%s placeholders are filled by
// the caller with fmt.Sprintf. Missing lang falls back to EN, then to the key.
var table = map[string]map[Lang]string{
	"health.ok":          {EN: "OK", UK: "OK"},
	"health.warn":        {EN: "warning", UK: "увага"},
	"health.down":        {EN: "service down", UK: "сервіс впав"},
	"health.unreachable": {EN: "ariadnectl unreachable", UK: "ariadnectl недоступний"},
	"row.records":        {EN: "Records", UK: "Записів"},
	"row.data":           {EN: "Data", UK: "Дані"},
	"row.free":           {EN: "free", UK: "вільно"},
	"status.up":          {EN: "up", UK: "працює"},
	"status.down":        {EN: "DOWN", UK: "не працює"},
	"status.ok":          {EN: "ariadne OK", UK: "ariadne OK"},
	"status.issues":      {EN: "ariadne ISSUES", UK: "ariadne ПРОБЛЕМИ"},
	"menu.start":         {EN: "▶ Start", UK: "▶ Старт"},
	"menu.stop":          {EN: "■ Stop", UK: "■ Стоп"},
	"menu.restart":       {EN: "⟳ Restart", UK: "⟳ Рестарт"},
	"menu.backup":        {EN: "💾 Back up now", UK: "💾 Бекап зараз"},
	"menu.export":        {EN: "⬇ Export (JSONL)", UK: "⬇ Експорт (JSONL)"},
	"menu.data":          {EN: "Show backups / data", UK: "Показати бекапи / дані"},
	"menu.logs":          {EN: "Show logs", UK: "Показати логи"},
	"menu.language":      {EN: "Language", UK: "Мова"},
	"menu.quit":          {EN: "Quit", UK: "Вийти"},
	"notify.backup":      {EN: "Backup", UK: "Бекап"},
	"notify.export":      {EN: "Export", UK: "Експорт"},
	"notify.done":        {EN: "done ✅", UK: "готово ✅"},
	"notify.failed":      {EN: "failed", UK: "помилка"},
	"issue.qdrant_down":  {EN: "Qdrant DOWN", UK: "Qdrant не працює"},
	"issue.ollama_down":  {EN: "Ollama DOWN", UK: "Ollama не працює"},
	"issue.coll_status":  {EN: "collection status: %s", UK: "стан колекції: %s"},
	"issue.low_disk":     {EN: "low disk: %dGB free", UK: "мало місця: %dГБ вільно"},
}

func langPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ariadne", "lang")
}

// Current is the active language: ~/.ariadne/lang, then $ARIADNE_LANG, else EN.
func Current() Lang {
	if b, err := os.ReadFile(langPath()); err == nil { //nolint:gosec // fixed path under $HOME
		if l := Lang(strings.TrimSpace(string(b))); Name[l] != "" {
			return l
		}
	}
	if l := Lang(os.Getenv("ARIADNE_LANG")); Name[l] != "" {
		return l
	}
	return EN
}

// Set persists the active language (written by the tray's switcher).
func Set(l Lang) error {
	home, _ := os.UserHomeDir()
	_ = os.MkdirAll(filepath.Join(home, ".ariadne"), 0o755) //nolint:gosec // user-owned
	return os.WriteFile(langPath(), []byte(l), 0o644)       //nolint:gosec // not a secret
}

// T translates key into lang, falling back to EN and then the raw key.
func T(lang Lang, key string) string {
	m, ok := table[key]
	if !ok {
		return key
	}
	if s, ok := m[lang]; ok {
		return s
	}
	return m[EN]
}
