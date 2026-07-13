# ariadne

**[English](README.md)** · **Українська**

**Нативний, локальний і багатомовний сервер довготривалої пам’яті** для
[Codex](https://github.com/openai/codex),
[Claude Code](https://claude.com/claude-code) та будь-якого MCP-клієнта.
[Go](https://go.dev/) + [Qdrant](https://qdrant.tech) +
[bge-m3](https://huggingface.co/BAAI/bge-m3) — без Docker, хмари й API-ключів.

[![Release](https://img.shields.io/github/v/release/mclaut/ariadne)](https://github.com/mclaut/ariadne/releases/latest)
[![CI](https://github.com/mclaut/ariadne/actions/workflows/ci.yml/badge.svg)](https://github.com/mclaut/ariadne/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-11120f.svg)](LICENSE)

**[Сайт проєкту українською](https://mclaut.github.io/ariadne/uk/)** ·
**[Простір Hugging Face](https://huggingface.co/spaces/mclaut/ariadne)** ·
**[Останній реліз](https://github.com/mclaut/ariadne/releases/latest)**

Ariadne замінює вбудовані векторні бази, які падають або блокуються під час
кількох паралельних MCP-сесій. Один сервер Qdrant нативно обробляє конкурентні
читання й записи, тому клас проблем single-writer та lock starvation зникає.

## Нове у v0.7.0

- **Точне отримання за ID.** `memory_recall` приймає content-hash `id` і повертає
  конкретний запис без embedding та приблизного ранжування.
- **Негайне збереження важливого.** Рішення, критичні референси, завершені звіти,
  релізи, деплої й перевірені результати записуються одразу. Чекати SessionEnd,
  PreCompact, щоденної консолідації або окремої команди не потрібно.
- **Пошук у межах кімнати.** Recall можна обмежити проєктом (`wing`), категорією
  (`room`) і колекцією.
- **Чесні метрики токенів.** Підтверджена економія, recall overhead і signed net
  показуються окремо; від’ємне net більше не називається «зекономленими токенами».

```json
{
  "id": "2704862554782470108"
}
```

## Як це працює

1. MCP-клієнт викликає `memory_save`, `memory_recall`, `memory_move` або
   `memory_delete`.
2. Ollama з bge-m3 створює багатомовний dense-вектор; BM25 зберігає точні терміни.
3. Qdrant об’єднує dense і sparse результати через RRF та зберігає дані локально.

Звичайний recall використовує кураторську колекцію `ariadne`. Сирі архіви сесій
лежать окремо в `sessions` і шукаються лише за явним запитом.

## Швидке встановлення

### macOS / Linux

```bash
curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh
```

### Windows

```powershell
irm https://raw.githubusercontent.com/mclaut/ariadne/main/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

Інсталятор:

- встановлює нативні binaries у `~/.ariadne/bin`;
- повторно використовує справні Qdrant та Ollama;
- прив’язує Qdrant лише до `127.0.0.1`;
- реєструє Ariadne для Codex, Claude Code або обох;
- встановлює skill негайного збереження довготривалої пам’яті;
- налаштовує резервні копії та щоденну консолідацію diary.

## Використання MCP

Зберегти рішення:

```json
{
  "wing": "my-project",
  "room": "decisions",
  "text": "Обрали PostgreSQL замість SQLite, тому що потрібні конкурентні записи."
}
```

Семантичний пошук:

```json
{
  "query": "чому обрали PostgreSQL",
  "wing": "my-project",
  "room": "decisions"
}
```

Точне отримання:

```json
{
  "id": "1234567890123456789"
}
```

## Операції

```bash
ariadnectl status
ariadnectl metrics
ariadnectl backup
ariadnectl export
ariadnectl consolidate --before 24h --dry-run
```

Метрики показують:

- **confirmed saved** — підтверджене повторне використання контексту;
- **recall overhead** — доставлений контекст без вимірюваної економії;
- **net** — signed різницю, яка може бути від’ємною.

## Приватність

- Qdrant працює лише на loopback і не має автентифікації за замовчуванням.
- Пам’ять зберігається у plaintext payload, тому секрети записувати не можна.
- Типовий стек використовує локальні Qdrant, Ollama та bge-m3.
- Віддалене створення session summary заблоковане без явного opt-in.

## Розробка

```bash
go test ./...
go build ./...
golangci-lint run
cd site && npm test
```

Повна англомовна документація й усі деталі архітектури доступні в
[README.md](README.md). Проєкт поширюється за ліцензією [MIT](LICENSE).
