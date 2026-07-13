"use client";

import {
  Apple,
  ArrowRight,
  ArrowUpRight,
  Check,
  Copy,
  Database,
  Github,
  Globe2,
  HardDrive,
  LockKeyhole,
  Monitor,
  Network,
  Server,
  ShieldCheck,
  Sparkles,
  Terminal,
} from "lucide-react";
import { useState } from "react";

const installs = {
  windows: {
    label: "Windows",
    icon: Monitor,
    command:
      "irm https://raw.githubusercontent.com/mclaut/ariadne/main/install.ps1 -OutFile install.ps1\npowershell -ExecutionPolicy Bypass -File .\\install.ps1",
    note:
      "Native x64 installer with explicit Claude, Codex, both, or core-only selection. No Docker; elevation is only for Qdrant's Microsoft VC++ runtime when needed.",
  },
  linux: {
    label: "Linux",
    icon: Terminal,
    command:
      "curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh",
    note: "Installs a loopback-only Qdrant user service and reuses the Ollama system service.",
  },
  macos: {
    label: "macOS",
    icon: Apple,
    command:
      "curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh",
    note: "Native Intel and Apple Silicon builds with Ollama Metal acceleration.",
  },
} as const;

type InstallKey = keyof typeof installs;

const jsonLd = {
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  name: "Ariadne",
  applicationCategory: "DeveloperApplication",
  operatingSystem: "Windows, macOS, Linux",
  softwareVersion: "0.6.0",
  description:
    "Local-first multilingual memory server for Codex, Claude Code, and MCP clients.",
  codeRepository: "https://github.com/mclaut/ariadne",
  license: "https://opensource.org/licenses/MIT",
  offers: { "@type": "Offer", price: "0", priceCurrency: "USD" },
};

export default function Home() {
  const [install, setInstall] = useState<InstallKey>("windows");
  const [copied, setCopied] = useState(false);
  const activeInstall = installs[install];

  async function copyInstall() {
    await navigator.clipboard.writeText(activeInstall.command);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }

  return (
    <main>
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }}
      />

      <nav className="site-nav" aria-label="Primary navigation">
        <a className="brand" href="#top" aria-label="Ariadne home">
          <span className="status-dot" />
          Ariadne
        </a>
        <div className="nav-links">
          <a href="#new">New in 0.6</a>
          <a href="#architecture">Architecture</a>
          <a href="#install">Install</a>
        </div>
        <a
          className="nav-github"
          href="https://github.com/mclaut/ariadne"
          target="_blank"
          rel="noreferrer"
        >
          <Github size={17} aria-hidden="true" />
          GitHub
        </a>
      </nav>

      <section className="hero" id="top">
        <div className="memory-field" aria-hidden="true">
          <div className="memory-map-head">
            <span>Local memory map</span>
            <span className="map-online"><i /> 4 ranked</span>
          </div>
          <div className="memory-map">
            <div className="map-card card-decision">decision / auth</div>
            <div className="map-card card-context">context / qdrant</div>
            <div className="map-card card-diary">diary / release</div>
            <div className="map-card card-fix">fix / windows</div>
            <div className="memory-spine">
              <div className="memory-hub">
                <Database size={22} />
                <strong>Ariadne</strong>
                <span>local</span>
              </div>
            </div>
          </div>
          <div className="memory-terminal">
            <span>$ memory_recall &quot;release decision&quot;</span>
            <strong>hybrid dense + sparse</strong>
            <span>4 memories ranked in 31ms</span>
          </div>
        </div>
        <div className="hero-content">
          <div className="release-kicker">
            <Sparkles size={16} aria-hidden="true" />
            v0.6.0 distills diary into durable memory
          </div>
          <h1>Ariadne</h1>
          <p className="hero-lead">
            Local-first memory for AI agents that need to remember across
            sessions, languages, and concurrent work.
          </p>
          <p className="hero-detail">
            Go, Qdrant, Ollama, and bge-m3. Your context stays on your machine.
            No cloud account, no API key, no embedded database lockups.
          </p>
          <div className="hero-actions">
            <a className="button button-primary" href="#install">
              Install Ariadne
              <ArrowRight size={18} aria-hidden="true" />
            </a>
            <a
              className="button button-secondary"
              href="https://github.com/mclaut/ariadne"
              target="_blank"
              rel="noreferrer"
            >
              View source
              <ArrowUpRight size={18} aria-hidden="true" />
            </a>
          </div>
        </div>
        <div className="hero-foot">
          <span>MIT licensed</span>
          <span>100+ languages</span>
          <span>Windows / macOS / Linux</span>
          <span>MCP stdio</span>
        </div>
      </section>

      <section className="new-band" id="new">
        <div className="section-shell">
          <div className="section-heading">
            <span className="eyebrow">New in v0.6.0</span>
            <h2>Keep conclusions, not chronology.</h2>
            <p>
              Daily local-model maintenance turns yesterday&apos;s session diary
              into compact decisions, verified fixes, and critical context.
            </p>
          </div>
          <div className="new-grid">
            <article className="new-item accent-green">
              <Check aria-hidden="true" />
              <h3>Automatic distillation</h3>
              <p>
                Group diary by project and day, then retain only knowledge that
                remains useful across future sessions.
              </p>
            </article>
            <article className="new-item accent-blue">
              <Server aria-hidden="true" />
              <h3>Fail-safe replacement</h3>
              <p>
                Snapshot first, save durable memories second, and retire source
                diary only after every write succeeds.
              </p>
            </article>
            <article className="new-item accent-coral">
              <Network aria-hidden="true" />
              <h3>Local and private</h3>
              <p>
                Use the existing local Ollama model. Remote summarization stays
                blocked unless explicitly enabled.
              </p>
            </article>
            <article className="new-item accent-black">
              <Monitor aria-hidden="true" />
              <h3>Preview before changing</h3>
              <p>
                Dry-run mode shows the exact decisions, gotchas, and references
                that would replace old diary entries.
              </p>
            </article>
          </div>
        </div>
      </section>

      <section className="architecture-band" id="architecture">
        <div className="section-shell">
          <div className="section-heading compact">
            <span className="eyebrow">Architecture</span>
            <h2>One memory service. Many agents.</h2>
            <p>
              Ariadne keeps MCP concerns at the edge and gives storage to a real
              server built for concurrent reads and writes.
            </p>
          </div>
          <ol className="architecture-flow">
            <li>
              <div className="step-number">01</div>
              <Network aria-hidden="true" />
              <h3>MCP clients</h3>
              <p>Codex, Claude Code, and any stdio-compatible client.</p>
              <code>save / recall / delete / move</code>
            </li>
            <li>
              <div className="step-number">02</div>
              <Globe2 aria-hidden="true" />
              <h3>bge-m3 + BM25</h3>
              <p>Multilingual dense meaning and exact sparse terms fused by RRF.</p>
              <code>Ollama localhost:11434</code>
            </li>
            <li>
              <div className="step-number">03</div>
              <Database aria-hidden="true" />
              <h3>Qdrant server</h3>
              <p>Durable vectors and plaintext payloads on loopback only.</p>
              <code>Qdrant localhost:6333/6334</code>
            </li>
          </ol>
        </div>
      </section>

      <section className="comparison-band">
        <div className="section-shell comparison-layout">
          <div className="section-heading compact">
            <span className="eyebrow">Why a server</span>
            <h2>Concurrent sessions should not fight over a file.</h2>
          </div>
          <div className="comparison-table" role="table" aria-label="Memory backend comparison">
            <div className="table-row table-head" role="row">
              <span role="columnheader">Behavior</span>
              <span role="columnheader">Embedded vector DB</span>
              <span role="columnheader">Ariadne</span>
            </div>
            <div className="table-row" role="row">
              <span role="cell">Concurrent writers</span>
              <span role="cell">Lock contention</span>
              <strong role="cell">Native server writes</strong>
            </div>
            <div className="table-row" role="row">
              <span role="cell">Cross-language recall</span>
              <span role="cell">Model dependent</span>
              <strong role="cell">bge-m3, 100+ languages</strong>
            </div>
            <div className="table-row" role="row">
              <span role="cell">Exact codes and names</span>
              <span role="cell">Dense-only drift</span>
              <strong role="cell">BM25 + dense RRF</strong>
            </div>
            <div className="table-row" role="row">
              <span role="cell">Data boundary</span>
              <span role="cell">Varies</span>
              <strong role="cell">Local and inspectable</strong>
            </div>
          </div>
        </div>
      </section>

      <section className="install-band" id="install">
        <div className="section-shell install-layout">
          <div className="section-heading compact install-copy">
            <span className="eyebrow">Install</span>
            <h2>Pick a platform. Keep the memory.</h2>
            <p>
              The installer reuses healthy Qdrant and Ollama services. Ariadne
              never exposes Qdrant beyond the local machine.
            </p>
            <div className="install-facts">
              <span><Check size={16} /> No Docker</span>
              <span><Check size={16} /> No cloud account</span>
              <span><Check size={16} /> Idempotent setup</span>
            </div>
          </div>

          <div className="install-tool">
            <div className="platform-tabs" role="tablist" aria-label="Install platform">
              {(Object.keys(installs) as InstallKey[]).map((key) => {
                const item = installs[key];
                const Icon = item.icon;
                return (
                  <button
                    key={key}
                    type="button"
                    role="tab"
                    aria-selected={install === key}
                    className={install === key ? "active" : ""}
                    onClick={() => {
                      setInstall(key);
                      setCopied(false);
                    }}
                  >
                    <Icon size={16} aria-hidden="true" />
                    {item.label}
                  </button>
                );
              })}
            </div>
            <div className="command-window">
              <div className="command-title">
                <span>PowerShell / terminal</span>
                <button
                  type="button"
                  className="icon-button"
                  onClick={copyInstall}
                  aria-label="Copy install command"
                  title="Copy install command"
                >
                  {copied ? <Check size={17} /> : <Copy size={17} />}
                </button>
              </div>
              <pre><code>{activeInstall.command}</code></pre>
            </div>
            <p className="install-note">{activeInstall.note}</p>
            <a
              className="release-link"
              href="https://github.com/mclaut/ariadne/releases/tag/v0.6.0"
              target="_blank"
              rel="noreferrer"
            >
              Release notes and manual downloads
              <ArrowUpRight size={16} aria-hidden="true" />
            </a>
          </div>
        </div>
      </section>

      <section className="security-band">
        <div className="section-shell security-layout">
          <LockKeyhole size={42} aria-hidden="true" />
          <div>
            <span className="eyebrow">Local-first by design</span>
            <h2>Your memory is not a telemetry stream.</h2>
          </div>
          <div className="security-points">
            <p><HardDrive size={18} /> Runtime and data live under your user home.</p>
            <p><Server size={18} /> Qdrant binds to 127.0.0.1 and has no public port.</p>
            <p><ShieldCheck size={18} /> Remote session summaries require explicit opt-in.</p>
          </div>
        </div>
      </section>

      <section className="faq-band">
        <div className="section-shell faq-layout">
          <div className="section-heading compact">
            <span className="eyebrow">Common questions</span>
            <h2>Short answers.</h2>
          </div>
          <div className="faq-list">
            <details>
              <summary>Does Ariadne send memory to a cloud service?</summary>
              <p>No. The default stack uses local Qdrant and local Ollama endpoints.</p>
            </details>
            <details>
              <summary>Can several Codex sessions use it at once?</summary>
              <p>Yes. Qdrant is a server and handles concurrent access natively.</p>
            </details>
            <details>
              <summary>Does English recall find Ukrainian notes?</summary>
              <p>Yes. bge-m3 provides cross-lingual embeddings across 100+ languages.</p>
            </details>
            <details>
              <summary>Can I move my memories later?</summary>
              <p>Yes. Export portable JSONL or create full Qdrant snapshots for recovery.</p>
            </details>
          </div>
        </div>
      </section>

      <footer>
        <div className="footer-brand">
          <span className="status-dot" />
          <strong>Ariadne</strong>
          <span>Local-first memory for AI agents.</span>
        </div>
        <div className="footer-links">
          <a href="https://github.com/mclaut/ariadne">GitHub</a>
          <a href="https://github.com/mclaut/ariadne/issues">Issues</a>
          <a href="https://github.com/mclaut/ariadne/blob/main/LICENSE">MIT License</a>
        </div>
      </footer>
    </main>
  );
}
