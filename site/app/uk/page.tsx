"use client";

import {
  ArrowRight,
  ArrowUpRight,
  Check,
  Database,
  Github,
  Globe2,
  HardDrive,
  LockKeyhole,
  Network,
  Server,
  ShieldCheck,
  Sparkles,
  Terminal,
} from "lucide-react";
import { useEffect } from "react";

export default function UkrainianHome() {
  useEffect(() => {
    document.documentElement.lang = "uk";
    window.localStorage.setItem("ariadne-language", "uk");
  }, []);

  return (
    <main>
      <nav className="site-nav" aria-label="Головна навігація">
        <a className="brand" href="#top" aria-label="Головна Ariadne">
          <span className="status-dot" /> Ariadne
        </a>
        <div className="nav-links">
          <a href="#new">Нове у 0.8.12</a>
          <a href="#architecture">Архітектура</a>
          <a href="#install">Встановлення</a>
        </div>
        <div className="nav-actions">
          <a
            className="language-switch"
            href="/ariadne/"
            hrefLang="en"
            onClick={() => window.localStorage.setItem("ariadne-language", "en")}
            aria-label="View in English"
          >
            <strong>EN</strong> <span>/</span> UA
          </a>
          <a className="nav-github" href="https://github.com/mclaut/ariadne">
            <Github size={17} aria-hidden="true" /> GitHub
          </a>
        </div>
      </nav>

      <section className="hero" id="top">
        <div className="memory-field" aria-hidden="true">
          <div className="memory-map-head"><span>Локальна карта пам’яті</span><span className="map-online"><i /> 4 знайдено</span></div>
          <div className="memory-map">
            <div className="map-card card-decision">рішення / авторизація</div>
            <div className="map-card card-context">контекст / qdrant</div>
            <div className="map-card card-diary">щоденник / реліз</div>
            <div className="map-card card-fix">виправлення / windows</div>
            <div className="memory-spine"><div className="memory-hub"><Database size={22} /><strong>Ariadne</strong><span>локально</span></div></div>
          </div>
          <div className="memory-terminal">
            <span>$ memory_move id=2704862554782470108 room=reference</span>
            <strong>новий scoped ID записано першим</strong>
            <span>оригінал збережено як superseded</span>
          </div>
        </div>
        <div className="hero-content">
          <div className="release-kicker"><Sparkles size={16} /> v0.8.12 робить maintenance передбачуваним</div>
          <h1>Ariadne</h1>
          <p className="hero-lead">Локальна пам’ять для AI-агентів, яким потрібно пам’ятати між сесіями, мовами та паралельними задачами.</p>
          <p className="hero-detail">Go, Qdrant, Ollama та bge-m3. Контекст залишається на вашому комп’ютері — без хмарного акаунта, API-ключа й тихого доступу до інших проєктів.</p>
          <div className="hero-actions">
            <a className="button button-primary" href="#install">Встановити Ariadne <ArrowRight size={18} /></a>
            <a className="button button-secondary" href="https://github.com/mclaut/ariadne">Переглянути код <ArrowUpRight size={18} /></a>
          </div>
        </div>
        <div className="hero-foot"><span>Ліцензія MIT</span><span>100+ мов</span><span>Windows / macOS / Linux</span><span>MCP stdio</span></div>
      </section>

      <section className="new-band" id="new">
        <div className="section-shell">
          <div className="section-heading"><span className="eyebrow">Нове у v0.8.12</span><h2>Надійний maintenance. Усвідомлене повторне використання credentials.</h2><p>Background memory care має межі, а повторне використання credential можливе лише за точним рішенням власника з аудитом.</p></div>
          <div className="new-grid">
            <article className="new-item accent-green"><ShieldCheck /><h3>Обмежено: scheduled maintenance</h3><p>20-хвилинний supervisor deadline не дає процесу, що не реагує, тримати щоденне завдання stuck годинами.</p></article>
            <article className="new-item accent-blue"><Database /><h3>Детерміновано: curator output</h3><p>Для schema-bound consolidation вимкнено thinking, тому reasoning-capable Ollama повертає очікуваний JSON.</p></article>
            <article className="new-item accent-coral"><Network /><h3>Scoped: credential trust</h3><p>Власник довіряє лише точним source, target, resource і purpose; кожне подальше використання все одно фіксується.</p></article>
            <article className="new-item accent-black"><Terminal /><h3>Зворотно: зміна policy</h3><p>Revoke додає нову append-only подію замість стирання історії аудиту.</p></article>
          </div>
        </div>
      </section>

      <section className="architecture-band" id="architecture">
        <div className="section-shell">
          <div className="section-heading compact"><span className="eyebrow">Архітектура</span><h2>Один сервіс пам’яті. Багато агентів.</h2><p>Ariadne залишає MCP на краю, а зберігання передає справжньому серверу для паралельного читання й запису.</p></div>
          <ol className="architecture-flow">
            <li><div className="step-number">01</div><Network /><h3>MCP-клієнти</h3><p>Codex, Claude Code та будь-який stdio-сумісний клієнт.</p><code>save / recall / delete / move / approve</code></li>
            <li><div className="step-number">02</div><Globe2 /><h3>bge-m3 + BM25</h3><p>Багатомовний зміст і точні терміни, об’єднані через RRF.</p><code>Ollama localhost:11434</code></li>
            <li><div className="step-number">03</div><Database /><h3>Сервер Qdrant</h3><p>Надійні вектори й текстові payload лише на loopback.</p><code>Qdrant localhost:6333/6334</code></li>
          </ol>
        </div>
      </section>

      <section className="install-band" id="install">
        <div className="section-shell install-layout">
          <div className="section-heading compact install-copy"><span className="eyebrow">Встановлення</span><h2>Оберіть платформу. Збережіть пам’ять.</h2><p>Інсталятор повторно використовує справні Qdrant та Ollama й ніколи не відкриває Qdrant назовні.</p><div className="install-facts"><span><Check size={16} /> Без Docker</span><span><Check size={16} /> Без хмарного акаунта</span><span><Check size={16} /> Ідемпотентне налаштування</span></div></div>
          <div className="install-tool">
            <div className="command-window"><div className="command-title"><span>macOS / Linux</span></div><pre><code>curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh</code></pre></div>
            <p className="install-note">Для Windows доступний нативний PowerShell-інсталятор. Готові збірки є у GitHub Release.</p>
            <a className="release-link" href="https://github.com/mclaut/ariadne/releases/tag/v0.8.12">Опис релізу та завантаження <ArrowUpRight size={16} /></a>
          </div>
        </div>
      </section>

      <section className="security-band">
        <div className="section-shell security-layout"><LockKeyhole size={42} /><div><span className="eyebrow">Локальність за задумом</span><h2>Ваша пам’ять — не потік телеметрії.</h2></div><div className="security-points"><p><HardDrive size={18} /> Runtime і дані зберігаються у домашній теці.</p><p><Server size={18} /> Qdrant прив’язаний до 127.0.0.1.</p><p><ShieldCheck size={18} /> Міжпроєктний доступ потребує видимого рішення людини.</p></div></div>
      </section>

      <footer><div className="footer-brand"><span className="status-dot" /><strong>Ariadne</strong><span>Локальна пам’ять для AI-агентів.</span></div><div className="footer-links"><a href="https://github.com/mclaut/ariadne">GitHub</a><a href="https://github.com/mclaut/ariadne/issues">Issues</a><a href="https://github.com/mclaut/ariadne/blob/main/LICENSE">Ліцензія MIT</a></div></footer>
    </main>
  );
}
