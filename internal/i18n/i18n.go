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
		"row.records":        "Records", "row.context_saved": "Measured token savings", "row.metrics_coverage": "Attribution coverage",
		"row.recalls": "Recalls", "row.unattributed": "Unattributed recall", "row.data": "Data", "row.free": "free",
		"row.maintenance": "Maintenance", "row.never": "never",
		"row.approvals": "Access requests", "approval.none": "none", "approval.cross": "Cross-project memory",
		"approval.protected":    "Protected resource access",
		"approval.prompt_title": "Ariadne access request", "approval.prompt_approve": "Approve",
		"approval.prompt_deny": "Deny", "approval.prompt_later": "Later",
		"approval.prompt_help": "Approve only if you initiated this request. Closing the window grants no access.",
		"status.up":            "up", "status.down": "DOWN", "status.ok": "ariadne OK", "status.issues": "ariadne ISSUES",
		"menu.start": "▶ Start", "menu.stop": "■ Stop", "menu.restart": "⟳ Restart",
		"menu.maintenance": "↻ Run maintenance now", "menu.maintenance_running": "Maintenance running…",
		"menu.approve": "✓ Approve", "menu.deny": "✕ Deny",
		"menu.backup": "💾 Back up now", "menu.export": "⬇ Export (JSONL)",
		"menu.data": "Show backups / data", "menu.logs": "Show logs",
		"menu.language": "Language", "menu.quit": "Quit", "menu.check_updates": "Check for updates",
		"menu.checking_updates": "Checking for updates…", "menu.update_to": "⬆ Update to %s",
		"menu.open_update": "↗ Open %s release", "menu.updating": "Updating to %s…",
		"notify.backup": "Backup", "notify.export": "Export", "notify.done": "done ✅", "notify.failed": "failed",
		"notify.maintenance": "Maintenance", "notify.started": "started",
		"notify.services": "Ariadne services", "notify.in_progress": "in progress…", "notify.verified": "verified",
		"notify.see_logs":     "See tray.log.",
		"notify.approval":     "Access approval",
		"notify.update_title": "Ariadne update", "notify.update_available": "%s is available. Open the tray to install it.",
		"notify.update_current": "Ariadne %s is up to date.", "notify.update_check_failed": "Could not check for updates.",
		"notify.update_installed": "Updated to %s.", "notify.update_failed": "Update to %s failed. See update.log.",
		"confirm.update_title": "Update Ariadne?", "confirm.update_body": "Install %s now? Ariadne will restart.",
		"confirm.update_yes": "Update", "confirm.update_no": "Cancel",
		"issue.qdrant_down": "Qdrant DOWN", "issue.ollama_down": "Ollama DOWN",
		"issue.qdrant_config":           "Qdrant configuration: %s",
		"issue.qdrant_duplicate_agents": "%d Ariadne Qdrant service jobs are loaded",
		"issue.qdrant_fd_pressure":      "Qdrant file descriptors near limit: %d/%d (%d%%)",
		"issue.coll_status":             "collection status: %s", "issue.low_disk": "low disk: %dGB free",
		"issue.metrics_error": "metrics unavailable", "issue.activity_error": "maintenance history unavailable",
		"issue.maintenance_failed": "maintenance failed: %s", "issue.maintenance_stale": "maintenance stale: last event %s",
		"issue.maintenance_degraded": "maintenance completed with deferred work: %s",
		"issue.maintenance_stuck":    "maintenance appears stuck since %s", "issue.maintenance_missing": "maintenance has never completed",
	},
	UK: {
		"health.ok": "OK", "health.warn": "увага", "health.down": "сервіс впав",
		"health.unreachable": "ariadnectl недоступний",
		"row.records":        "Записів", "row.context_saved": "Виміряно зекономлено", "row.metrics_coverage": "Покриття атрибуції",
		"row.recalls": "Звернень", "row.unattributed": "Без атрибуції", "row.data": "Дані", "row.free": "вільно",
		"row.maintenance": "Обслуговування", "row.never": "ще не запускалось",
		"row.approvals": "Запити доступу", "approval.none": "немає", "approval.cross": "Міжпроєктна пам’ять",
		"approval.protected":    "Доступ до захищеного ресурсу",
		"approval.prompt_title": "Запит доступу Ariadne", "approval.prompt_approve": "Схвалити",
		"approval.prompt_deny": "Відхилити", "approval.prompt_later": "Пізніше",
		"approval.prompt_help": "Схвалюйте лише запити, які ініціювали ви. Закриття вікна не надає доступу.",
		"status.up":            "працює", "status.down": "не працює", "status.ok": "ariadne OK", "status.issues": "ariadne ПРОБЛЕМИ",
		"menu.start": "▶ Старт", "menu.stop": "■ Стоп", "menu.restart": "⟳ Рестарт",
		"menu.maintenance": "↻ Запустити обслуговування", "menu.maintenance_running": "Обслуговування виконується…",
		"menu.approve": "✓ Схвалити", "menu.deny": "✕ Відхилити",
		"menu.backup": "💾 Бекап зараз", "menu.export": "⬇ Експорт (JSONL)",
		"menu.data": "Показати бекапи / дані", "menu.logs": "Показати логи",
		"menu.language": "Мова", "menu.quit": "Вийти", "menu.check_updates": "Перевірити оновлення",
		"menu.checking_updates": "Перевіряю оновлення…", "menu.update_to": "⬆ Оновити до %s",
		"menu.open_update": "↗ Відкрити реліз %s", "menu.updating": "Оновлення до %s…",
		"notify.backup": "Бекап", "notify.export": "Експорт", "notify.done": "готово ✅", "notify.failed": "помилка",
		"notify.maintenance": "Обслуговування", "notify.started": "запущено",
		"notify.services": "Сервіси Ariadne", "notify.in_progress": "виконується…", "notify.verified": "перевірено",
		"notify.see_logs":     "Див. tray.log.",
		"notify.approval":     "Підтвердження доступу",
		"notify.update_title": "Оновлення Ariadne", "notify.update_available": "Доступна %s. Відкрийте tray, щоб установити.",
		"notify.update_current": "Ariadne %s уже актуальна.", "notify.update_check_failed": "Не вдалося перевірити оновлення.",
		"notify.update_installed": "Оновлено до %s.", "notify.update_failed": "Оновлення до %s не вдалося. Див. update.log.",
		"confirm.update_title": "Оновити Ariadne?", "confirm.update_body": "Установити %s зараз? Ariadne перезапуститься.",
		"confirm.update_yes": "Оновити", "confirm.update_no": "Скасувати",
		"issue.qdrant_down": "Qdrant не працює", "issue.ollama_down": "Ollama не працює",
		"issue.qdrant_config":           "Налаштування Qdrant: %s",
		"issue.qdrant_duplicate_agents": "завантажено %d сервісних jobs Ariadne Qdrant",
		"issue.qdrant_fd_pressure":      "дескриптори Qdrant майже вичерпано: %d/%d (%d%%)",
		"issue.coll_status":             "стан колекції: %s", "issue.low_disk": "мало місця: %dГБ вільно",
		"issue.metrics_error": "метрики недоступні", "issue.activity_error": "історія maintenance недоступна",
		"issue.maintenance_failed":   "обслуговування завершилось помилкою: %s",
		"issue.maintenance_degraded": "обслуговування завершене з відкладеною роботою: %s",
		"issue.maintenance_stale":    "обслуговування застаріло: остання подія %s",
		"issue.maintenance_stuck":    "обслуговування, схоже, зависло з %s",
		"issue.maintenance_missing":  "обслуговування ще жодного разу не завершилось",
	},
	DE: {
		"health.ok": "OK", "health.warn": "Warnung", "health.down": "Dienst ausgefallen",
		"health.unreachable": "ariadnectl nicht erreichbar",
		"row.records":        "Einträge", "row.context_saved": "Gemessene Token-Einsparung", "row.metrics_coverage": "Attributionsabdeckung",
		"row.recalls": "Abrufe", "row.unattributed": "Nicht zugeordnet", "row.data": "Daten", "row.free": "frei",
		"row.maintenance": "Wartung", "row.never": "nie",
		"row.approvals": "Zugriffsanfragen", "approval.none": "keine", "approval.cross": "Projektübergreifender Speicher",
		"approval.protected":    "Zugriff auf geschützte Ressource",
		"approval.prompt_title": "Ariadne-Zugriffsanfrage", "approval.prompt_approve": "Genehmigen",
		"approval.prompt_deny": "Ablehnen", "approval.prompt_later": "Später",
		"approval.prompt_help": "Nur selbst initiierte Anfragen genehmigen. Schließen oder Später gewährt keinen Zugriff.",
		"status.up":            "läuft", "status.down": "aus", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEME",
		"menu.start": "▶ Start", "menu.stop": "■ Stopp", "menu.restart": "⟳ Neustart",
		"menu.maintenance": "↻ Wartung jetzt ausführen", "menu.maintenance_running": "Wartung läuft…",
		"menu.approve": "✓ Genehmigen", "menu.deny": "✕ Ablehnen",
		"menu.backup": "💾 Jetzt sichern", "menu.export": "⬇ Export (JSONL)",
		"menu.data": "Backups / Daten anzeigen", "menu.logs": "Logs anzeigen",
		"menu.language": "Sprache", "menu.quit": "Beenden", "menu.check_updates": "Nach Updates suchen",
		"menu.checking_updates": "Suche nach Updates…", "menu.update_to": "⬆ Auf %s aktualisieren",
		"menu.open_update": "↗ Release %s öffnen", "menu.updating": "Aktualisiere auf %s…",
		"notify.backup": "Backup", "notify.export": "Export", "notify.done": "fertig ✅", "notify.failed": "fehlgeschlagen",
		"notify.maintenance": "Wartung", "notify.started": "gestartet",
		"notify.services": "Ariadne-Dienste", "notify.in_progress": "wird ausgeführt…", "notify.verified": "verifiziert",
		"notify.see_logs":     "Siehe tray.log.",
		"notify.approval":     "Zugriffsgenehmigung",
		"notify.update_title": "Ariadne-Update", "notify.update_available": "%s ist verfügbar. Zum Installieren das Tray-Menü öffnen.",
		"notify.update_current": "Ariadne %s ist aktuell.", "notify.update_check_failed": "Updates konnten nicht geprüft werden.",
		"notify.update_installed": "Auf %s aktualisiert.", "notify.update_failed": "Update auf %s fehlgeschlagen. Siehe update.log.",
		"confirm.update_title": "Ariadne aktualisieren?", "confirm.update_body": "%s jetzt installieren? Ariadne wird neu gestartet.",
		"confirm.update_yes": "Aktualisieren", "confirm.update_no": "Abbrechen",
		"issue.qdrant_down": "Qdrant aus", "issue.ollama_down": "Ollama aus",
		"issue.qdrant_config":           "Qdrant-Konfiguration: %s",
		"issue.qdrant_duplicate_agents": "%d Ariadne-Qdrant-Dienste sind geladen",
		"issue.qdrant_fd_pressure":      "Qdrant-Dateideskriptoren fast am Limit: %d/%d (%d%%)",
		"issue.coll_status":             "Sammlungsstatus: %s", "issue.low_disk": "wenig Speicher: %dGB frei",
		"issue.metrics_error": "Metriken nicht verfügbar", "issue.activity_error": "Wartungsverlauf nicht verfügbar",
		"issue.maintenance_failed": "Wartung fehlgeschlagen: %s", "issue.maintenance_stale": "Wartung veraltet: letztes Ereignis %s",
		"issue.maintenance_degraded": "Wartung mit zurückgestellter Arbeit beendet: %s",
		"issue.maintenance_stuck":    "Wartung hängt offenbar seit %s", "issue.maintenance_missing": "Wartung wurde noch nie abgeschlossen",
	},
	IT: {
		"health.ok": "OK", "health.warn": "attenzione", "health.down": "servizio inattivo",
		"health.unreachable": "ariadnectl irraggiungibile",
		"row.records":        "Record", "row.context_saved": "Risparmio token misurato", "row.metrics_coverage": "Copertura attribuzione",
		"row.recalls": "Richiami", "row.unattributed": "Non attribuito", "row.data": "Dati", "row.free": "liberi",
		"row.maintenance": "Manutenzione", "row.never": "mai",
		"row.approvals": "Richieste di accesso", "approval.none": "nessuna", "approval.cross": "Memoria tra progetti",
		"approval.protected":    "Accesso a risorsa protetta",
		"approval.prompt_title": "Richiesta di accesso Ariadne", "approval.prompt_approve": "Approva",
		"approval.prompt_deny": "Nega", "approval.prompt_later": "Più tardi",
		"approval.prompt_help": "Approva solo le richieste avviate da te. Chiudere o scegliere Più tardi non concede accesso.",
		"status.up":            "attivo", "status.down": "inattivo", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMI",
		"menu.start": "▶ Avvia", "menu.stop": "■ Arresta", "menu.restart": "⟳ Riavvia",
		"menu.maintenance": "↻ Esegui manutenzione ora", "menu.maintenance_running": "Manutenzione in corso…",
		"menu.approve": "✓ Approva", "menu.deny": "✕ Nega",
		"menu.backup": "💾 Backup ora", "menu.export": "⬇ Esporta (JSONL)",
		"menu.data": "Mostra backup / dati", "menu.logs": "Mostra log",
		"menu.language": "Lingua", "menu.quit": "Esci", "menu.check_updates": "Controlla aggiornamenti",
		"menu.checking_updates": "Controllo aggiornamenti…", "menu.update_to": "⬆ Aggiorna a %s",
		"menu.open_update": "↗ Apri la versione %s", "menu.updating": "Aggiornamento a %s…",
		"notify.backup": "Backup", "notify.export": "Esportazione", "notify.done": "fatto ✅", "notify.failed": "non riuscito",
		"notify.maintenance": "Manutenzione", "notify.started": "avviata",
		"notify.services": "Servizi Ariadne", "notify.in_progress": "operazione in corso…", "notify.verified": "verificato",
		"notify.see_logs":     "Consulta tray.log.",
		"notify.approval":     "Approvazione accesso",
		"notify.update_title": "Aggiornamento Ariadne", "notify.update_available": "%s è disponibile. Apri il menu tray per installarla.",
		"notify.update_current": "Ariadne %s è aggiornata.", "notify.update_check_failed": "Impossibile controllare gli aggiornamenti.",
		"notify.update_installed": "Aggiornata a %s.", "notify.update_failed": "Aggiornamento a %s non riuscito. Vedi update.log.",
		"confirm.update_title": "Aggiornare Ariadne?", "confirm.update_body": "Installare %s ora? Ariadne verrà riavviata.",
		"confirm.update_yes": "Aggiorna", "confirm.update_no": "Annulla",
		"issue.qdrant_down": "Qdrant inattivo", "issue.ollama_down": "Ollama inattivo",
		"issue.qdrant_config":           "Configurazione Qdrant: %s",
		"issue.qdrant_duplicate_agents": "sono caricati %d servizi Ariadne Qdrant",
		"issue.qdrant_fd_pressure":      "descrittori Qdrant vicini al limite: %d/%d (%d%%)",
		"issue.coll_status":             "stato collezione: %s", "issue.low_disk": "spazio scarso: %dGB liberi",
		"issue.metrics_error": "metriche non disponibili", "issue.activity_error": "cronologia manutenzione non disponibile",
		"issue.maintenance_failed": "manutenzione non riuscita: %s", "issue.maintenance_stale": "manutenzione obsoleta: ultimo evento %s",
		"issue.maintenance_degraded": "manutenzione completata con lavoro rinviato: %s",
		"issue.maintenance_stuck":    "la manutenzione sembra bloccata dal %s",
		"issue.maintenance_missing":  "la manutenzione non è mai stata completata",
	},
	ES: {
		"health.ok": "OK", "health.warn": "advertencia", "health.down": "servicio caído",
		"health.unreachable": "ariadnectl inaccesible",
		"row.records":        "Registros", "row.context_saved": "Ahorro de tokens medido", "row.metrics_coverage": "Cobertura atribuida",
		"row.recalls": "Consultas", "row.unattributed": "Sin atribuir", "row.data": "Datos", "row.free": "libres",
		"row.maintenance": "Mantenimiento", "row.never": "nunca",
		"row.approvals": "Solicitudes de acceso", "approval.none": "ninguna", "approval.cross": "Memoria entre proyectos",
		"approval.protected":    "Acceso a recurso protegido",
		"approval.prompt_title": "Solicitud de acceso de Ariadne", "approval.prompt_approve": "Aprobar",
		"approval.prompt_deny": "Denegar", "approval.prompt_later": "Más tarde",
		"approval.prompt_help": "Aprueba solo solicitudes iniciadas por ti. Cerrar o elegir Más tarde no concede acceso.",
		"status.up":            "activo", "status.down": "inactivo", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMAS",
		"menu.start": "▶ Iniciar", "menu.stop": "■ Detener", "menu.restart": "⟳ Reiniciar",
		"menu.maintenance": "↻ Ejecutar mantenimiento ahora", "menu.maintenance_running": "Mantenimiento en curso…",
		"menu.approve": "✓ Aprobar", "menu.deny": "✕ Denegar",
		"menu.backup": "💾 Copia ahora", "menu.export": "⬇ Exportar (JSONL)",
		"menu.data": "Mostrar copias / datos", "menu.logs": "Mostrar registros",
		"menu.language": "Idioma", "menu.quit": "Salir", "menu.check_updates": "Buscar actualizaciones",
		"menu.checking_updates": "Buscando actualizaciones…", "menu.update_to": "⬆ Actualizar a %s",
		"menu.open_update": "↗ Abrir versión %s", "menu.updating": "Actualizando a %s…",
		"notify.backup": "Copia", "notify.export": "Exportación", "notify.done": "hecho ✅", "notify.failed": "fallido",
		"notify.maintenance": "Mantenimiento", "notify.started": "iniciado",
		"notify.services": "Servicios de Ariadne", "notify.in_progress": "en curso…", "notify.verified": "verificado",
		"notify.see_logs":     "Consulta tray.log.",
		"notify.approval":     "Aprobación de acceso",
		"notify.update_title": "Actualización de Ariadne", "notify.update_available": "%s está disponible. Abre el menú tray para instalarla.",
		"notify.update_current": "Ariadne %s está actualizada.", "notify.update_check_failed": "No se pudieron buscar actualizaciones.",
		"notify.update_installed": "Actualizada a %s.", "notify.update_failed": "La actualización a %s falló. Consulta update.log.",
		"confirm.update_title": "¿Actualizar Ariadne?", "confirm.update_body": "¿Instalar %s ahora? Ariadne se reiniciará.",
		"confirm.update_yes": "Actualizar", "confirm.update_no": "Cancelar",
		"issue.qdrant_down": "Qdrant caído", "issue.ollama_down": "Ollama caído",
		"issue.qdrant_config":           "Configuración de Qdrant: %s",
		"issue.qdrant_duplicate_agents": "hay %d servicios Ariadne Qdrant cargados",
		"issue.qdrant_fd_pressure":      "descriptores de Qdrant cerca del límite: %d/%d (%d%%)",
		"issue.coll_status":             "estado de colección: %s", "issue.low_disk": "poco espacio: %dGB libres",
		"issue.metrics_error": "métricas no disponibles", "issue.activity_error": "historial de mantenimiento no disponible",
		"issue.maintenance_failed": "mantenimiento fallido: %s", "issue.maintenance_stale": "mantenimiento obsoleto: último evento %s",
		"issue.maintenance_degraded": "mantenimiento completado con trabajo aplazado: %s",
		"issue.maintenance_stuck":    "el mantenimiento parece bloqueado desde %s",
		"issue.maintenance_missing":  "el mantenimiento nunca se ha completado",
	},
	FR: {
		"health.ok": "OK", "health.warn": "avertissement", "health.down": "service arrêté",
		"health.unreachable": "ariadnectl injoignable",
		"row.records":        "Entrées", "row.context_saved": "Économie de tokens mesurée", "row.metrics_coverage": "Couverture attribuée",
		"row.recalls": "Rappels", "row.unattributed": "Non attribué", "row.data": "Données", "row.free": "libre",
		"row.maintenance": "Maintenance", "row.never": "jamais",
		"row.approvals": "Demandes d’accès", "approval.none": "aucune", "approval.cross": "Mémoire interprojets",
		"approval.protected":    "Accès à la ressource protégée",
		"approval.prompt_title": "Demande d’accès Ariadne", "approval.prompt_approve": "Approuver",
		"approval.prompt_deny": "Refuser", "approval.prompt_later": "Plus tard",
		"approval.prompt_help": "N’approuvez que les demandes que vous avez initiées. Fermer ou choisir Plus tard n’accorde aucun accès.",
		"status.up":            "actif", "status.down": "arrêté", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLÈMES",
		"menu.start": "▶ Démarrer", "menu.stop": "■ Arrêter", "menu.restart": "⟳ Redémarrer",
		"menu.maintenance": "↻ Lancer la maintenance", "menu.maintenance_running": "Maintenance en cours…",
		"menu.approve": "✓ Approuver", "menu.deny": "✕ Refuser",
		"menu.backup": "💾 Sauvegarder", "menu.export": "⬇ Exporter (JSONL)",
		"menu.data": "Afficher sauvegardes / données", "menu.logs": "Afficher les journaux",
		"menu.language": "Langue", "menu.quit": "Quitter", "menu.check_updates": "Rechercher les mises à jour",
		"menu.checking_updates": "Recherche des mises à jour…", "menu.update_to": "⬆ Mettre à jour vers %s",
		"menu.open_update": "↗ Ouvrir la version %s", "menu.updating": "Mise à jour vers %s…",
		"notify.backup": "Sauvegarde", "notify.export": "Export", "notify.done": "terminé ✅", "notify.failed": "échec",
		"notify.maintenance": "Maintenance", "notify.started": "démarrée",
		"notify.services": "Services Ariadne", "notify.in_progress": "en cours…", "notify.verified": "vérifié",
		"notify.see_logs":     "Voir tray.log.",
		"notify.approval":     "Approbation d’accès",
		"notify.update_title": "Mise à jour d’Ariadne", "notify.update_available": "%s est disponible. Ouvrez le menu tray pour l’installer.",
		"notify.update_current": "Ariadne %s est à jour.", "notify.update_check_failed": "Impossible de rechercher les mises à jour.",
		"notify.update_installed": "Mise à jour vers %s terminée.", "notify.update_failed": "Échec de la mise à jour vers %s. Voir update.log.",
		"confirm.update_title": "Mettre Ariadne à jour ?", "confirm.update_body": "Installer %s maintenant ? Ariadne redémarrera.",
		"confirm.update_yes": "Mettre à jour", "confirm.update_no": "Annuler",
		"issue.qdrant_down": "Qdrant arrêté", "issue.ollama_down": "Ollama arrêté",
		"issue.qdrant_config":           "Configuration Qdrant : %s",
		"issue.qdrant_duplicate_agents": "%d services Ariadne Qdrant sont chargés",
		"issue.qdrant_fd_pressure":      "descripteurs Qdrant proches de la limite : %d/%d (%d%%)",
		"issue.coll_status":             "état collection : %s", "issue.low_disk": "disque faible : %dGo libres",
		"issue.metrics_error": "métriques indisponibles", "issue.activity_error": "historique de maintenance indisponible",
		"issue.maintenance_failed": "échec de la maintenance : %s", "issue.maintenance_stale": "maintenance obsolète : dernier événement %s",
		"issue.maintenance_degraded": "maintenance terminée avec du travail reporté : %s",
		"issue.maintenance_stuck":    "la maintenance semble bloquée depuis %s",
		"issue.maintenance_missing":  "la maintenance n’a jamais été terminée",
	},
	PL: {
		"health.ok": "OK", "health.warn": "ostrzeżenie", "health.down": "usługa nie działa",
		"health.unreachable": "ariadnectl niedostępny",
		"row.records":        "Wpisy", "row.context_saved": "Zmierzona oszczędność tokenów", "row.metrics_coverage": "Pokrycie atrybucji",
		"row.recalls": "Odczyty", "row.unattributed": "Bez atrybucji", "row.data": "Dane", "row.free": "wolne",
		"row.maintenance": "Konserwacja", "row.never": "nigdy",
		"row.approvals": "Żądania dostępu", "approval.none": "brak", "approval.cross": "Pamięć między projektami",
		"approval.protected":    "Dostęp do chronionego zasobu",
		"approval.prompt_title": "Żądanie dostępu Ariadne", "approval.prompt_approve": "Zatwierdź",
		"approval.prompt_deny": "Odrzuć", "approval.prompt_later": "Później",
		"approval.prompt_help": "Zatwierdzaj tylko własne żądania. Zamknięcie lub wybór Później nie przyznaje dostępu.",
		"status.up":            "działa", "status.down": "nie działa", "status.ok": "ariadne OK", "status.issues": "ariadne PROBLEMY",
		"menu.start": "▶ Start", "menu.stop": "■ Zatrzymaj", "menu.restart": "⟳ Restart",
		"menu.maintenance": "↻ Uruchom konserwację", "menu.maintenance_running": "Konserwacja trwa…",
		"menu.approve": "✓ Zatwierdź", "menu.deny": "✕ Odrzuć",
		"menu.backup": "💾 Kopia teraz", "menu.export": "⬇ Eksport (JSONL)",
		"menu.data": "Pokaż kopie / dane", "menu.logs": "Pokaż logi",
		"menu.language": "Język", "menu.quit": "Zakończ", "menu.check_updates": "Sprawdź aktualizacje",
		"menu.checking_updates": "Sprawdzanie aktualizacji…", "menu.update_to": "⬆ Aktualizuj do %s",
		"menu.open_update": "↗ Otwórz wydanie %s", "menu.updating": "Aktualizacja do %s…",
		"notify.backup": "Kopia", "notify.export": "Eksport", "notify.done": "gotowe ✅", "notify.failed": "błąd",
		"notify.maintenance": "Konserwacja", "notify.started": "uruchomiona",
		"notify.services": "Usługi Ariadne", "notify.in_progress": "w toku…", "notify.verified": "zweryfikowano",
		"notify.see_logs":     "Zobacz tray.log.",
		"notify.approval":     "Zatwierdzenie dostępu",
		"notify.update_title": "Aktualizacja Ariadne", "notify.update_available": "%s jest dostępna. Otwórz menu tray, aby ją zainstalować.",
		"notify.update_current": "Ariadne %s jest aktualna.", "notify.update_check_failed": "Nie udało się sprawdzić aktualizacji.",
		"notify.update_installed": "Zaktualizowano do %s.", "notify.update_failed": "Aktualizacja do %s nie powiodła się. Zobacz update.log.",
		"confirm.update_title": "Zaktualizować Ariadne?", "confirm.update_body": "Zainstalować %s teraz? Ariadne uruchomi się ponownie.",
		"confirm.update_yes": "Aktualizuj", "confirm.update_no": "Anuluj",
		"issue.qdrant_down": "Qdrant nie działa", "issue.ollama_down": "Ollama nie działa",
		"issue.qdrant_config":           "Konfiguracja Qdrant: %s",
		"issue.qdrant_duplicate_agents": "załadowano %d usługi Ariadne Qdrant",
		"issue.qdrant_fd_pressure":      "deskryptory Qdrant blisko limitu: %d/%d (%d%%)",
		"issue.coll_status":             "stan kolekcji: %s", "issue.low_disk": "mało miejsca: %dGB wolne",
		"issue.metrics_error": "metryki niedostępne", "issue.activity_error": "historia konserwacji niedostępna",
		"issue.maintenance_failed":   "konserwacja nie powiodła się: %s",
		"issue.maintenance_degraded": "konserwacja zakończona z odroczoną pracą: %s",
		"issue.maintenance_stale":    "konserwacja nieaktualna: ostatnie zdarzenie %s",
		"issue.maintenance_stuck":    "konserwacja wygląda na zawieszoną od %s",
		"issue.maintenance_missing":  "konserwacja nigdy nie została ukończona",
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
