// AriadneMonitor — menu-bar monitor for the ariadne stack (Qdrant + Ollama +
// the ariadne MCP server). Thin viewer: it shells `ariadnectl status -json`
// and renders; all logic lives in the Go core. AppKit NSStatusItem (not SwiftUI
// MenuBarExtra — emoji labels render reliably; autosaveName fixes notch overflow).

import AppKit

enum Config {
    // Runtime home is ~/.ariadne — outside TCC-protected folders (Desktop/Documents),
    // so launchd agents can exec/read everything without Full Disk Access.
    static let ctl = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".ariadne/bin/ariadnectl").path
    static let dataDir = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".ariadne/qdrant-data")
    static let backupsDir = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".ariadne/backups")
    static let logsDir = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent(".ariadne/logs")
}

struct Status {
    var ok = false
    var qdrantUp = false, ollamaUp = false
    var qdrantRSS = 0, ollamaRSS = 0
    var ollamaVer = "", collStatus = "?"
    var points = 0, dataMB = 0, freeGB = 0
    var issues: [String] = []
    var reachable = false // did ariadnectl run at all
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var timer: Timer?
    private var lastIssues: [String] = []

    private let rowHealth = NSMenuItem(title: "…", action: nil, keyEquivalent: "")
    private let rowQdrant = NSMenuItem(title: "", action: nil, keyEquivalent: "")
    private let rowOllama = NSMenuItem(title: "", action: nil, keyEquivalent: "")
    private let rowPoints = NSMenuItem(title: "", action: nil, keyEquivalent: "")
    private let rowDisk = NSMenuItem(title: "", action: nil, keyEquivalent: "")

    func applicationDidFinishLaunching(_: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.autosaveName = "AriadneMonitor"
        statusItem.button?.title = "⚫"
        statusItem.button?.toolTip = "ariadne monitor"
        buildMenu()
        poll()
        timer = Timer.scheduledTimer(withTimeInterval: 5.0, repeats: true) { [weak self] _ in self?.poll() }
    }

    private func buildMenu() {
        let m = NSMenu()
        for r in [rowHealth, rowQdrant, rowOllama, rowPoints, rowDisk] {
            r.isEnabled = false
            m.addItem(r)
        }
        m.addItem(.separator())
        m.addItem(withTitle: "▶ Старт", action: #selector(startSvc), keyEquivalent: "").target = self
        m.addItem(withTitle: "■ Стоп", action: #selector(stopSvc), keyEquivalent: "").target = self
        m.addItem(withTitle: "⟳ Рестарт", action: #selector(restartSvc), keyEquivalent: "").target = self
        m.addItem(.separator())
        m.addItem(withTitle: "💾 Бекап зараз", action: #selector(backupNow), keyEquivalent: "").target = self
        m.addItem(withTitle: "⬇ Експорт (JSONL)", action: #selector(exportNow), keyEquivalent: "").target = self
        m.addItem(withTitle: "Показати бекапи/дані", action: #selector(openData), keyEquivalent: "").target = self
        m.addItem(withTitle: "Показати логи", action: #selector(openLogs), keyEquivalent: "").target = self
        m.addItem(.separator())
        m.addItem(withTitle: "Вийти", action: #selector(quit), keyEquivalent: "q").target = self
        statusItem.menu = m
    }

    private func poll() {
        let s = fetch()
        let icon: String, word: String
        if !s.reachable {
            icon = "⚫"; word = "ariadnectl недоступний"
        } else if !s.qdrantUp || !s.ollamaUp {
            icon = "🔴"; word = "сервіс впав"
        } else if !s.issues.isEmpty {
            icon = "🟠"; word = "увага"
        } else {
            icon = "🟢"; word = "OK"
        }
        statusItem.button?.title = icon
        rowHealth.title = "ariadne — \(word)"
        rowQdrant.title = "Qdrant: \(s.qdrantUp ? "up" : "DOWN") · \(s.qdrantRSS)MB"
        rowOllama.title = "Ollama: \(s.ollamaUp ? "up \(s.ollamaVer)" : "DOWN") · \(s.ollamaRSS)MB"
        rowPoints.title = "Записів: \(grouped(s.points)) (\(s.collStatus))"
        rowDisk.title = "Дані: \(s.dataMB)MB · вільно \(s.freeGB)GB"
        if s.issues.isEmpty {
            rowDisk.title += ""
        }

        // notify when NEW issues appear (or a service dropped)
        if s.reachable, s.issues != lastIssues, !s.issues.isEmpty {
            notify("⚠️ ariadne", s.issues.joined(separator: " · "))
        }
        lastIssues = s.issues
    }

    // MARK: actions
    @objc private func startSvc() { runCtl("start") }
    @objc private func stopSvc() { runCtl("stop") }
    @objc private func restartSvc() { runCtl("restart") }
    @objc private func backupNow() { runCtl("backup", notify: "Бекап") }
    @objc private func exportNow() { runCtl("export", notify: "Експорт") }
    @objc private func openData() { NSWorkspace.shared.open(Config.backupsDir) }
    @objc private func openLogs() { NSWorkspace.shared.open(Config.logsDir) }
    @objc private func quit() { NSApp.terminate(nil) }

    private func runCtl(_ action: String, notify banner: String? = nil) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: Config.ctl)
        p.arguments = [action]
        if let banner {
            p.terminationHandler = { [weak self] proc in
                DispatchQueue.main.async {
                    self?.notify("ariadne", "\(banner): \(proc.terminationStatus == 0 ? "готово ✅" : "помилка")")
                }
            }
        }
        try? p.run()
    }

    // MARK: helpers
    private func fetch() -> Status {
        var s = Status()
        let p = Process()
        p.executableURL = URL(fileURLWithPath: Config.ctl)
        p.arguments = ["status", "-json"]
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = FileHandle.nullDevice
        do { try p.run() } catch { return s }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        guard let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return s }
        s.reachable = true
        s.ok = (obj["ok"] as? Bool) ?? false
        if let q = obj["qdrant"] as? [String: Any] {
            s.qdrantUp = (q["up"] as? Bool) ?? false
            s.qdrantRSS = (q["rss_mb"] as? Int) ?? 0
        }
        if let o = obj["ollama"] as? [String: Any] {
            s.ollamaUp = (o["up"] as? Bool) ?? false
            s.ollamaRSS = (o["rss_mb"] as? Int) ?? 0
            s.ollamaVer = (o["version"] as? String) ?? ""
        }
        if let c = obj["collection"] as? [String: Any] {
            s.points = (c["points"] as? Int) ?? 0
            s.collStatus = (c["status"] as? String) ?? "?"
        }
        s.dataMB = (obj["data_mb"] as? Int) ?? 0
        s.freeGB = (obj["free_gb"] as? Int) ?? 0
        s.issues = (obj["issues"] as? [String]) ?? []
        return s
    }

    private func notify(_ title: String, _ msg: String) {
        let script = "display notification \(q(msg)) with title \(q(title)) sound name \"Basso\""
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", script]
        try? p.run()
    }
    private func q(_ s: String) -> String { "\"" + s.replacingOccurrences(of: "\"", with: "'") + "\"" }

    private func grouped(_ n: Int) -> String {
        let f = NumberFormatter()
        f.numberStyle = .decimal
        f.groupingSeparator = " "
        return f.string(from: NSNumber(value: n)) ?? "\(n)"
    }
}

@main
struct Main {
    static func main() {
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        let delegate = AppDelegate()
        app.delegate = delegate
        app.run()
    }
}
