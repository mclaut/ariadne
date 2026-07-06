// Package i18n localizes Ariadne's user-facing interface (tray + ariadnectl).
// The active language persists in ~/.ariadne/lang — the tray's switcher writes
// it, and every binary reads it, so the whole UI stays in one language.
//
// Adding a language: add its block to `table` (copy EN and translate), plus a
// Name + Flag entry, then list it in Available. Nothing else changes — the tray
// picks it up automatically. Missing keys fall back to English.
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
	DE Lang = "de"
	IT Lang = "it"
	ES Lang = "es"
	FR Lang = "fr"
	PL Lang = "pl"
)

// Available is the switch order shown in the UI. Name = native endonym, Flag =
// a recognizable emoji so the switcher reads at a glance in any language.
var (
	Available = []Lang{EN, UK, DE, IT, ES, FR, PL}
	Name      = map[Lang]string{EN: "English", UK: "Українська", DE: "Deutsch", IT: "Italiano", ES: "Español", FR: "Français", PL: "Polski"}
	Flag      = map[Lang]string{EN: "🇬🇧", UK: "🇺🇦", DE: "🇩🇪", IT: "🇮🇹", ES: "🇪🇸", FR: "🇫🇷", PL: "🇵🇱"}
)

// table[lang][key]. %d/%s placeholders are filled by the caller via Sprintf.
var table = map[Lang]map[string]string{
	EN: {
		"health.ok": "OK", "health.warn": "warning", "health.down": "service down",
		"health.unreachable": "ariadnectl unreachable",
		"row.records":        "Records", "row.data": "Data", "row.free": "free",
		"status.up": "up", "status.down": "DOWN", "status.ok": "ariadne OK", "status.issues": "ariadne ISSUES",
		"menu.start": "▶ Start", "menu.stop": "■ Stop", "menu.restart": "⟳ Restart",
		"menu.backup": "💾 Back up now", "menu.export": "⬇ Export (JSONL)",
		"menu.data": "Show backups / data", "menu.logs": "Show logs",
		"menu.language": "Language", "menu.quit": "Quit",
		"notify.backup": "Backup", "notify.export": "Export", "notify.done": "done ✅", "notify.failed": "failed",
		"issue.qdrant_down": "Qdrant DOWN", "issue.ollama_down": "Ollama DOWN",
		"issue.coll_status": "collection status: %s", "issue.low_disk": "low disk: %dGB free",
	},
	UK: {
		"health.ok": "OK", "health.warn": "увага", "health.down": "сервіс впав",
		"health.unreachable": "ariadnectl недоступний",
		"row.records":        "Записів", "row.data": "Дані", "row.free": "вільно",
		"status.up": "працює", "status.down": "не працює", "status.ok": "ariadne OK", "status.issues": "ariadne ПРОБЛЕМИ",
		"menu.start": "▶ Старт", "menu.stop": "■ Стоп", "menu.restart": "⟳ Рестарт",
		"menu.backup": "💾 Бекап зараз", "menu.export": "⬇ Експорт (JSONL)",
		"menu.data": "Показати бекапи / дані", "menu.logs": "Показати логи",
		"menu.language": "Мова", "menu.quit": "Вийти",
		"notify.backup": "Бекап", "notify.export": "Експорт", "notify.done": "готово ✅", "notify.failed": "помилка",
		"issue.qdrant_down": "Qdrant не працює", "issue.ollama_down": "Ollama не працює",
		"issue.coll_status": "стан колекції: %s", "issue.low_disk": "мало місця: %dГБ вільно",
	},
	DE: {
		"health.ok": "OK", "health.warn": "Warnung", "health.down": "Dienst ausgefallen",
		"health.unreachable": "ariadnectl nicht erreichbar",
		"row.records":        "Einträge", "row.data": "Daten", "row.free": "frei",
		"status.up": "läuft", "status.down": "aus", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEME",
		"menu.start": "▶ Start", "menu.stop": "■ Stopp", "menu.restart": "⟳ Neustart",
		"menu.backup": "💾 Jetzt sichern", "menu.export": "⬇ Export (JSONL)",
		"menu.data": "Backups / Daten anzeigen", "menu.logs": "Logs anzeigen",
		"menu.language": "Sprache", "menu.quit": "Beenden",
		"notify.backup": "Backup", "notify.export": "Export", "notify.done": "fertig ✅", "notify.failed": "fehlgeschlagen",
		"issue.qdrant_down": "Qdrant aus", "issue.ollama_down": "Ollama aus",
		"issue.coll_status": "Sammlungsstatus: %s", "issue.low_disk": "wenig Speicher: %dGB frei",
	},
	IT: {
		"health.ok": "OK", "health.warn": "attenzione", "health.down": "servizio inattivo",
		"health.unreachable": "ariadnectl irraggiungibile",
		"row.records":        "Record", "row.data": "Dati", "row.free": "liberi",
		"status.up": "attivo", "status.down": "inattivo", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMI",
		"menu.start": "▶ Avvia", "menu.stop": "■ Arresta", "menu.restart": "⟳ Riavvia",
		"menu.backup": "💾 Backup ora", "menu.export": "⬇ Esporta (JSONL)",
		"menu.data": "Mostra backup / dati", "menu.logs": "Mostra log",
		"menu.language": "Lingua", "menu.quit": "Esci",
		"notify.backup": "Backup", "notify.export": "Esportazione", "notify.done": "fatto ✅", "notify.failed": "non riuscito",
		"issue.qdrant_down": "Qdrant inattivo", "issue.ollama_down": "Ollama inattivo",
		"issue.coll_status": "stato collezione: %s", "issue.low_disk": "spazio scarso: %dGB liberi",
	},
	ES: {
		"health.ok": "OK", "health.warn": "advertencia", "health.down": "servicio caído",
		"health.unreachable": "ariadnectl inaccesible",
		"row.records":        "Registros", "row.data": "Datos", "row.free": "libres",
		"status.up": "activo", "status.down": "inactivo", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMAS",
		"menu.start": "▶ Iniciar", "menu.stop": "■ Detener", "menu.restart": "⟳ Reiniciar",
		"menu.backup": "💾 Copia ahora", "menu.export": "⬇ Exportar (JSONL)",
		"menu.data": "Mostrar copias / datos", "menu.logs": "Mostrar registros",
		"menu.language": "Idioma", "menu.quit": "Salir",
		"notify.backup": "Copia", "notify.export": "Exportación", "notify.done": "hecho ✅", "notify.failed": "fallido",
		"issue.qdrant_down": "Qdrant caído", "issue.ollama_down": "Ollama caído",
		"issue.coll_status": "estado de colección: %s", "issue.low_disk": "poco espacio: %dGB libres",
	},
	FR: {
		"health.ok": "OK", "health.warn": "avertissement", "health.down": "service arrêté",
		"health.unreachable": "ariadnectl injoignable",
		"row.records":        "Entrées", "row.data": "Données", "row.free": "libre",
		"status.up": "actif", "status.down": "arrêté", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLÈMES",
		"menu.start": "▶ Démarrer", "menu.stop": "■ Arrêter", "menu.restart": "⟳ Redémarrer",
		"menu.backup": "💾 Sauvegarder", "menu.export": "⬇ Exporter (JSONL)",
		"menu.data": "Afficher sauvegardes / données", "menu.logs": "Afficher les journaux",
		"menu.language": "Langue", "menu.quit": "Quitter",
		"notify.backup": "Sauvegarde", "notify.export": "Export", "notify.done": "terminé ✅", "notify.failed": "échec",
		"issue.qdrant_down": "Qdrant arrêté", "issue.ollama_down": "Ollama arrêté",
		"issue.coll_status": "état collection : %s", "issue.low_disk": "disque faible : %dGo libres",
	},
	PL: {
		"health.ok": "OK", "health.warn": "ostrzeżenie", "health.down": "usługa nie działa",
		"health.unreachable": "ariadnectl niedostępny",
		"row.records":        "Wpisy", "row.data": "Dane", "row.free": "wolne",
		"status.up": "działa", "status.down": "nie działa", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMY",
		"menu.start": "▶ Start", "menu.stop": "■ Zatrzymaj", "menu.restart": "⟳ Restart",
		"menu.backup": "💾 Kopia teraz", "menu.export": "⬇ Eksport (JSONL)",
		"menu.data": "Pokaż kopie / dane", "menu.logs": "Pokaż logi",
		"menu.language": "Język", "menu.quit": "Zakończ",
		"notify.backup": "Kopia", "notify.export": "Eksport", "notify.done": "gotowe ✅", "notify.failed": "błąd",
		"issue.qdrant_down": "Qdrant nie działa", "issue.ollama_down": "Ollama nie działa",
		"issue.coll_status": "stan kolekcji: %s", "issue.low_disk": "mało miejsca: %dGB wolne",
	},
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

// T translates key into lang, falling back to English and then the raw key.
func T(lang Lang, key string) string {
	if m, ok := table[lang]; ok {
		if s, ok := m[key]; ok {
			return s
		}
	}
	if s, ok := table[EN][key]; ok {
		return s
	}
	return key
}
