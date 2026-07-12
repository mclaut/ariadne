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
		"row.records":        "Records", "row.context_saved": "Tokens saved", "row.data": "Data", "row.free": "free",
		"status.up": "up", "status.down": "DOWN", "status.ok": "ariadne OK", "status.issues": "ariadne ISSUES",
		"menu.start": "▶ Start", "menu.stop": "■ Stop", "menu.restart": "⟳ Restart",
		"menu.backup": "💾 Back up now", "menu.export": "⬇ Export (JSONL)",
		"menu.data": "Show backups / data", "menu.logs": "Show logs",
		"menu.language": "Language", "menu.quit": "Quit", "menu.check_updates": "Check for updates",
		"menu.checking_updates": "Checking for updates…", "menu.update_to": "⬆ Update to %s",
		"menu.open_update": "↗ Open %s release", "menu.updating": "Updating to %s…",
		"notify.backup": "Backup", "notify.export": "Export", "notify.done": "done ✅", "notify.failed": "failed",
		"notify.update_title": "Ariadne update", "notify.update_available": "%s is available. Open the tray to install it.",
		"notify.update_current": "Ariadne %s is up to date.", "notify.update_check_failed": "Could not check for updates.",
		"notify.update_installed": "Updated to %s.", "notify.update_failed": "Update to %s failed. See update.log.",
		"confirm.update_title": "Update Ariadne?", "confirm.update_body": "Install %s now? Ariadne will restart.",
		"confirm.update_yes": "Update", "confirm.update_no": "Cancel",
		"issue.qdrant_down": "Qdrant DOWN", "issue.ollama_down": "Ollama DOWN",
		"issue.coll_status": "collection status: %s", "issue.low_disk": "low disk: %dGB free",
	},
	UK: {
		"health.ok": "OK", "health.warn": "увага", "health.down": "сервіс впав",
		"health.unreachable": "ariadnectl недоступний",
		"row.records":        "Записів", "row.context_saved": "Зекономлено токенів", "row.data": "Дані", "row.free": "вільно",
		"status.up": "працює", "status.down": "не працює", "status.ok": "ariadne OK", "status.issues": "ariadne ПРОБЛЕМИ",
		"menu.start": "▶ Старт", "menu.stop": "■ Стоп", "menu.restart": "⟳ Рестарт",
		"menu.backup": "💾 Бекап зараз", "menu.export": "⬇ Експорт (JSONL)",
		"menu.data": "Показати бекапи / дані", "menu.logs": "Показати логи",
		"menu.language": "Мова", "menu.quit": "Вийти", "menu.check_updates": "Перевірити оновлення",
		"menu.checking_updates": "Перевіряю оновлення…", "menu.update_to": "⬆ Оновити до %s",
		"menu.open_update": "↗ Відкрити реліз %s", "menu.updating": "Оновлення до %s…",
		"notify.backup": "Бекап", "notify.export": "Експорт", "notify.done": "готово ✅", "notify.failed": "помилка",
		"notify.update_title": "Оновлення Ariadne", "notify.update_available": "Доступна %s. Відкрийте tray, щоб установити.",
		"notify.update_current": "Ariadne %s уже актуальна.", "notify.update_check_failed": "Не вдалося перевірити оновлення.",
		"notify.update_installed": "Оновлено до %s.", "notify.update_failed": "Оновлення до %s не вдалося. Див. update.log.",
		"confirm.update_title": "Оновити Ariadne?", "confirm.update_body": "Установити %s зараз? Ariadne перезапуститься.",
		"confirm.update_yes": "Оновити", "confirm.update_no": "Скасувати",
		"issue.qdrant_down": "Qdrant не працює", "issue.ollama_down": "Ollama не працює",
		"issue.coll_status": "стан колекції: %s", "issue.low_disk": "мало місця: %dГБ вільно",
	},
	DE: {
		"health.ok": "OK", "health.warn": "Warnung", "health.down": "Dienst ausgefallen",
		"health.unreachable": "ariadnectl nicht erreichbar",
		"row.records":        "Einträge", "row.context_saved": "Gesparte Tokens", "row.data": "Daten", "row.free": "frei",
		"status.up": "läuft", "status.down": "aus", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEME",
		"menu.start": "▶ Start", "menu.stop": "■ Stopp", "menu.restart": "⟳ Neustart",
		"menu.backup": "💾 Jetzt sichern", "menu.export": "⬇ Export (JSONL)",
		"menu.data": "Backups / Daten anzeigen", "menu.logs": "Logs anzeigen",
		"menu.language": "Sprache", "menu.quit": "Beenden", "menu.check_updates": "Nach Updates suchen",
		"menu.checking_updates": "Suche nach Updates…", "menu.update_to": "⬆ Auf %s aktualisieren",
		"menu.open_update": "↗ Release %s öffnen", "menu.updating": "Aktualisiere auf %s…",
		"notify.backup": "Backup", "notify.export": "Export", "notify.done": "fertig ✅", "notify.failed": "fehlgeschlagen",
		"notify.update_title": "Ariadne-Update", "notify.update_available": "%s ist verfügbar. Zum Installieren das Tray-Menü öffnen.",
		"notify.update_current": "Ariadne %s ist aktuell.", "notify.update_check_failed": "Updates konnten nicht geprüft werden.",
		"notify.update_installed": "Auf %s aktualisiert.", "notify.update_failed": "Update auf %s fehlgeschlagen. Siehe update.log.",
		"confirm.update_title": "Ariadne aktualisieren?", "confirm.update_body": "%s jetzt installieren? Ariadne wird neu gestartet.",
		"confirm.update_yes": "Aktualisieren", "confirm.update_no": "Abbrechen",
		"issue.qdrant_down": "Qdrant aus", "issue.ollama_down": "Ollama aus",
		"issue.coll_status": "Sammlungsstatus: %s", "issue.low_disk": "wenig Speicher: %dGB frei",
	},
	IT: {
		"health.ok": "OK", "health.warn": "attenzione", "health.down": "servizio inattivo",
		"health.unreachable": "ariadnectl irraggiungibile",
		"row.records":        "Record", "row.context_saved": "Token risparmiati", "row.data": "Dati", "row.free": "liberi",
		"status.up": "attivo", "status.down": "inattivo", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMI",
		"menu.start": "▶ Avvia", "menu.stop": "■ Arresta", "menu.restart": "⟳ Riavvia",
		"menu.backup": "💾 Backup ora", "menu.export": "⬇ Esporta (JSONL)",
		"menu.data": "Mostra backup / dati", "menu.logs": "Mostra log",
		"menu.language": "Lingua", "menu.quit": "Esci", "menu.check_updates": "Controlla aggiornamenti",
		"menu.checking_updates": "Controllo aggiornamenti…", "menu.update_to": "⬆ Aggiorna a %s",
		"menu.open_update": "↗ Apri la versione %s", "menu.updating": "Aggiornamento a %s…",
		"notify.backup": "Backup", "notify.export": "Esportazione", "notify.done": "fatto ✅", "notify.failed": "non riuscito",
		"notify.update_title": "Aggiornamento Ariadne", "notify.update_available": "%s è disponibile. Apri il menu tray per installarla.",
		"notify.update_current": "Ariadne %s è aggiornata.", "notify.update_check_failed": "Impossibile controllare gli aggiornamenti.",
		"notify.update_installed": "Aggiornata a %s.", "notify.update_failed": "Aggiornamento a %s non riuscito. Vedi update.log.",
		"confirm.update_title": "Aggiornare Ariadne?", "confirm.update_body": "Installare %s ora? Ariadne verrà riavviata.",
		"confirm.update_yes": "Aggiorna", "confirm.update_no": "Annulla",
		"issue.qdrant_down": "Qdrant inattivo", "issue.ollama_down": "Ollama inattivo",
		"issue.coll_status": "stato collezione: %s", "issue.low_disk": "spazio scarso: %dGB liberi",
	},
	ES: {
		"health.ok": "OK", "health.warn": "advertencia", "health.down": "servicio caído",
		"health.unreachable": "ariadnectl inaccesible",
		"row.records":        "Registros", "row.context_saved": "Tokens ahorrados", "row.data": "Datos", "row.free": "libres",
		"status.up": "activo", "status.down": "inactivo", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMAS",
		"menu.start": "▶ Iniciar", "menu.stop": "■ Detener", "menu.restart": "⟳ Reiniciar",
		"menu.backup": "💾 Copia ahora", "menu.export": "⬇ Exportar (JSONL)",
		"menu.data": "Mostrar copias / datos", "menu.logs": "Mostrar registros",
		"menu.language": "Idioma", "menu.quit": "Salir", "menu.check_updates": "Buscar actualizaciones",
		"menu.checking_updates": "Buscando actualizaciones…", "menu.update_to": "⬆ Actualizar a %s",
		"menu.open_update": "↗ Abrir versión %s", "menu.updating": "Actualizando a %s…",
		"notify.backup": "Copia", "notify.export": "Exportación", "notify.done": "hecho ✅", "notify.failed": "fallido",
		"notify.update_title": "Actualización de Ariadne", "notify.update_available": "%s está disponible. Abre el menú tray para instalarla.",
		"notify.update_current": "Ariadne %s está actualizada.", "notify.update_check_failed": "No se pudieron buscar actualizaciones.",
		"notify.update_installed": "Actualizada a %s.", "notify.update_failed": "La actualización a %s falló. Consulta update.log.",
		"confirm.update_title": "¿Actualizar Ariadne?", "confirm.update_body": "¿Instalar %s ahora? Ariadne se reiniciará.",
		"confirm.update_yes": "Actualizar", "confirm.update_no": "Cancelar",
		"issue.qdrant_down": "Qdrant caído", "issue.ollama_down": "Ollama caído",
		"issue.coll_status": "estado de colección: %s", "issue.low_disk": "poco espacio: %dGB libres",
	},
	FR: {
		"health.ok": "OK", "health.warn": "avertissement", "health.down": "service arrêté",
		"health.unreachable": "ariadnectl injoignable",
		"row.records":        "Entrées", "row.context_saved": "Tokens économisés", "row.data": "Données", "row.free": "libre",
		"status.up": "actif", "status.down": "arrêté", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLÈMES",
		"menu.start": "▶ Démarrer", "menu.stop": "■ Arrêter", "menu.restart": "⟳ Redémarrer",
		"menu.backup": "💾 Sauvegarder", "menu.export": "⬇ Exporter (JSONL)",
		"menu.data": "Afficher sauvegardes / données", "menu.logs": "Afficher les journaux",
		"menu.language": "Langue", "menu.quit": "Quitter", "menu.check_updates": "Rechercher les mises à jour",
		"menu.checking_updates": "Recherche des mises à jour…", "menu.update_to": "⬆ Mettre à jour vers %s",
		"menu.open_update": "↗ Ouvrir la version %s", "menu.updating": "Mise à jour vers %s…",
		"notify.backup": "Sauvegarde", "notify.export": "Export", "notify.done": "terminé ✅", "notify.failed": "échec",
		"notify.update_title": "Mise à jour d’Ariadne", "notify.update_available": "%s est disponible. Ouvrez le menu tray pour l’installer.",
		"notify.update_current": "Ariadne %s est à jour.", "notify.update_check_failed": "Impossible de rechercher les mises à jour.",
		"notify.update_installed": "Mise à jour vers %s terminée.", "notify.update_failed": "Échec de la mise à jour vers %s. Voir update.log.",
		"confirm.update_title": "Mettre Ariadne à jour ?", "confirm.update_body": "Installer %s maintenant ? Ariadne redémarrera.",
		"confirm.update_yes": "Mettre à jour", "confirm.update_no": "Annuler",
		"issue.qdrant_down": "Qdrant arrêté", "issue.ollama_down": "Ollama arrêté",
		"issue.coll_status": "état collection : %s", "issue.low_disk": "disque faible : %dGo libres",
	},
	PL: {
		"health.ok": "OK", "health.warn": "ostrzeżenie", "health.down": "usługa nie działa",
		"health.unreachable": "ariadnectl niedostępny",
		"row.records":        "Wpisy", "row.context_saved": "Zaoszczędzone tokeny", "row.data": "Dane", "row.free": "wolne",
		"status.up": "działa", "status.down": "nie działa", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMY",
		"menu.start": "▶ Start", "menu.stop": "■ Zatrzymaj", "menu.restart": "⟳ Restart",
		"menu.backup": "💾 Kopia teraz", "menu.export": "⬇ Eksport (JSONL)",
		"menu.data": "Pokaż kopie / dane", "menu.logs": "Pokaż logi",
		"menu.language": "Język", "menu.quit": "Zakończ", "menu.check_updates": "Sprawdź aktualizacje",
		"menu.checking_updates": "Sprawdzanie aktualizacji…", "menu.update_to": "⬆ Aktualizuj do %s",
		"menu.open_update": "↗ Otwórz wydanie %s", "menu.updating": "Aktualizacja do %s…",
		"notify.backup": "Kopia", "notify.export": "Eksport", "notify.done": "gotowe ✅", "notify.failed": "błąd",
		"notify.update_title": "Aktualizacja Ariadne", "notify.update_available": "%s jest dostępna. Otwórz menu tray, aby ją zainstalować.",
		"notify.update_current": "Ariadne %s jest aktualna.", "notify.update_check_failed": "Nie udało się sprawdzić aktualizacji.",
		"notify.update_installed": "Zaktualizowano do %s.", "notify.update_failed": "Aktualizacja do %s nie powiodła się. Zobacz update.log.",
		"confirm.update_title": "Zaktualizować Ariadne?", "confirm.update_body": "Zainstalować %s teraz? Ariadne uruchomi się ponownie.",
		"confirm.update_yes": "Aktualizuj", "confirm.update_no": "Anuluj",
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
