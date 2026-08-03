# ariadne

**[English](README.md)** · **Українська**

**Нативний, локальний і багатомовний сервер довготривалої пам’яті** для
[Codex](https://github.com/openai/codex),
[Claude Code](https://claude.com/claude-code) та будь-якого MCP-клієнта.
[Go](https://go.dev/) + [Qdrant](https://qdrant.tech) +
[bge-m3](https://huggingface.co/BAAI/bge-m3) — без Docker, хмари й API-ключів.

Ariadne спеціально сфокусована на приватній пам’яті coding agents: це невеликий
нативний appliance, а не хмарна multi-tenant платформа. Типовий шлях повністю
локальний, багатомовний і не потребує акаунта.

[![Release](https://img.shields.io/github/v/release/mclaut/ariadne)](https://github.com/mclaut/ariadne/releases/latest)
[![CI](https://github.com/mclaut/ariadne/actions/workflows/ci.yml/badge.svg)](https://github.com/mclaut/ariadne/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-11120f.svg)](LICENSE)

**[Сайт проєкту українською](https://mclaut.github.io/ariadne/uk/)** ·
**[Простір Hugging Face](https://huggingface.co/spaces/mclaut/ariadne)** ·
**[Останній реліз](https://github.com/mclaut/ariadne/releases/latest)**

Ariadne замінює вбудовані векторні бази, які падають або блокуються під час
кількох паралельних MCP-сесій. Один сервер Qdrant нативно обробляє конкурентні
читання й записи, тому клас проблем single-writer та lock starvation зникає.

## Нове у v0.8.1

- **Один власник Qdrant.** Status виявляє дубльовані macOS launchd jobs, а
  `ariadnectl` та installer нормалізують керування до одного канонічного job без
  видалення даних, старих plist чи попередніх runtime-версій.
- **Розумний maintenance.** Кожна captured-сесія консолідується окремо, а
  same-day результати потім об'єднуються й дедуплікуються. Локальний quality
  gate відхиляє пам'ять, яка змішує незалежні теми, після чого модель отримує
  одну цільову repair-спробу. Безпечно відкладені записи отримують revision
  marker і не запускаються повторно з тією самою моделлю та версією pipeline;
  `complete_with_deferred` лишається видимим, але не фарбує справний tray у
  помаранчевий.
- **Tray із пояснюваним життєвим циклом.** Після аварійного виходу він
  перезапускається, але явний Quit залишається чистим завершенням. Причини start
  та exit записуються в лог.
- **Doctor, якому можна вірити.** Він знаходить активний immutable runtime,
  перевіряє Codex і Claude MCP, maintenance, launchd ownership, attribution
  coverage, storage та runaway logs і розрізняє green, degraded та failed.

## Раніше у v0.8.0

- **Scoped append-only історія.** Однаковий текст може незалежно існувати в
  різних проєктах і кімнатах. Move спершу створює цільовий запис і залишає
  оригінал як superseded history. Capture окремо зберігає час події та запису;
  consolidate спершу створює durable memories, а джерельні diary лише позначає
  як `archived`.
- **Історичні ваги.** Після dense+BM25 RRF застосовується невеликий обмежений
  rerank за temporal intent, provenance і розміром контексту. Старі рішення не
  втрачають вагу лише через вік; recency діє, коли сам запит просить останній або
  поточний стан.
- **Аудит і безпечний sync.** `include_archived: true` повертає архівні та
  superseded записи. Memfile sync пропускає незмінені файли без embedding,
  додає нову ревізію перед supersede старої, а зниклі джерела позначає
  `orphaned`, не видаляючи історію.
- **Надійний maintenance.** Обмежена за batch і context консолідація перевіряє
  структуру та якість відповіді, а source diary лишається активним після
  помилки. Runner робить до трьох спроб із capped backoff; status і tray
  показують failed, partial, stuck та stale стани й дозволяють запустити процес
  негайно.
- **Regression evaluation.** `go run ./cmd/eval` запускає read-only набір
  багатомовних coding-memory сценаріїв без Qdrant та Ollama.

## Раніше у v0.7.0

- **Точне отримання за ID.** `memory_recall` приймає content-hash `id` і повертає
  конкретний запис без embedding та приблизного ранжування.
- **Негайне збереження важливого.** Рішення, критичні референси, завершені звіти,
  релізи, деплої й перевірені результати записуються одразу. Чекати SessionEnd,
  PreCompact, щоденної консолідації або окремої команди не потрібно.
- **Пошук у межах кімнати.** Recall можна обмежити проєктом (`wing`), категорією
  (`room`) і колекцією.
- **Чесні метрики токенів.** Виміряна економія, coverage, кількість recall і
  unattributed-витрати показуються окремо; невідоме джерело більше не створює
  вигаданий негативний net.

```json
{
  "id": "2704862554782470108"
}
```

## Як це працює

1. MCP-клієнт викликає `memory_save`, `memory_recall`, `memory_move` або
   `memory_delete`.
2. Ollama з bge-m3 створює багатомовний dense-вектор; BM25 зберігає точні терміни.
3. Qdrant об’єднує dense і sparse результати через RRF; обмежений другий прохід
   враховує явний часовий намір, provenance та завеликий контекст.

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
- налаштовує резервні копії та retry-bounded maintenance щодня о 04:30.

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

Пошук в архівній історії:

```json
{
  "query": "попереднє рішення щодо консолідації",
  "wing": "my-project",
  "include_archived": true
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
go run ./cmd/eval -cases evaluation/coding-memory.json
```

Метрики показують:

- **measured saved / net** — економію лише для пам’ятей із відомим розміром джерела;
- **attribution coverage** — частку recall-витрат, яку можна зіставити з джерелом;
- **unattributed** — доставку старих і ручних пам’ятей без вигаданого негативного net;
- **recalls** — кількість фактичних отримань пам’яті.

Повторний recall тієї самої пам’яті в одній клієнтській сесії вдруге не отримує
credit за джерело, але його вартість рахується. `memory_save` має опційний
`source_tokens` для інтеграцій, які знають розмір конкретного стисненого джерела;
capture і consolidate додають provenance автоматично.

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
go run ./cmd/eval
cd site && npm test
```

Повна англомовна документація й усі деталі архітектури доступні в
[README.md](README.md). Проєкт поширюється за ліцензією [MIT](LICENSE).
