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

## Нове у v0.8.15

### Виправлено

- **Ariadne тепер є звичайною програмою macOS.** Інсталятор створює підписану
  `Ariadne.app` у системній теці Applications, якщо вона доступна для запису,
  або в `~/Applications`, і додає нативну іконку проєкту.
- **Один клік відновлює керований tray.** Запуск через Finder, Spotlight або
  Launchpad запускає наявний tray LaunchAgent і за потреби повторно реєструє
  canonical plist без некерованого дубля процесу.

## Раніше у v0.8.14

v0.8.14 відокремила підтверджений helper оновлення macOS від завершення tray і
зробила handoff видимим у `update.log`.

## Раніше у v0.8.13

v0.8.13 перетворила deadline consolidation на безпечне deferred-відкладення,
зберігаючи кожен source diary за допомогою append-only метаданих.

## Раніше у v0.8.12

v0.8.12 обмежила maintenance supervisor, зробила schema-bound consolidation
JSON детермінованим для reasoning-capable Ollama моделей і додала точну
append-only credential-прив’язку, схвалену власником.

## Раніше у v0.8.11

v0.8.11 зробила desktop tray справді single-instance, узгодила canonical та
історичний launchd ownership, зберегла superseded plist в archive і додала
перевірку реальної кількості tray-процесів.

## Раніше у v0.8.10

v0.8.10 зробила token attribution аудитованою завдяки lossless Qdrant pagination,
відокремленню recallable memory від history, measured provenance від estimates,
явній класифікації gaps і безпечному opt-in attribution backfill.

## Раніше у v0.8.9

### Виправлено

- **Qdrant залишається доступним за багатьох паралельних agent sessions.** Кожен
  MCP-процес тепер використовує одне постійне gRPC-з’єднання замість неявного
  пулу з трьох, а macOS launchd отримує явний ліміт 8192 descriptors замість
  успадкованих 256.
- **Startup більше не повторює створення всіх payload indexes.** Ariadne читає
  наявну schema та створює лише відсутні indexes. Реальні storage errors тепер
  зупиняють startup, а не ігноруються.
- **Короткотривалі клієнти закриваються коректно.** Hooks, import, install і
  maintenance звільняють Qdrant connection після завершення.

### Додано

- **Видимість descriptor pressure.** `ariadnectl status`, tray і doctor
  показують відкриті descriptors та налаштований limit і попереджають до
  вичерпання ресурсу.

## Раніше у v0.8.8

### Додано

- **Fail-closed автентифікація віддаленого Qdrant.** Remote gRPC потребує API
  key і TLS, а REST — той самий key та HTTPS. Довготривалі клієнти зберігають
  лише захищений шлях до key-файлу, а не саме значення. Явний insecure override
  залишається для SSH або іншого тунелю під контролем користувача.
- **Чесне порівняння retrieval.** `cmd/eval` обчислює детерміновані macro
  Recall, MRR і nDCG для judged BM25 та learned-sparse runs без непідтверджених
  заяв про перевагу SPLADE.

### Змінено

- **Metrics schema v3 масштабується без втрати історії.** Усі recall events
  лишаються append-only, а індексований 30-денний шлях і транзакційні денні
  rollups роблять lifetime totals швидкими. Міграція v2 не змінює raw rows або
  підсумки.
- **Collection scan став повним.** Memfile reconciliation читає всі сторінки
  Qdrant замість фіксованої верхньої межі.
- **Maintenance отримав reusable core.** Retry/backoff orchestration винесено в
  `internal/maintenance` зі збереженням CLI та activity semantics.

### Виправлено

- **Remote settings переживають install і self-update.** macOS, Linux та
  Windows launchers передають однакові не-секретні Qdrant settings, а явні
  Windows installer arguments мають пріоритет.
- **Go tooling більше не компілює залежності з `site/node_modules`.** Сайт має
  окремий Go module, а `make clean` архівує generated assets з recovery
  manifest замість видалення.

## Раніше у v0.8.7

### Виправлено

- **Restart tray працює в обмеженому середовищі launchd.** Якщо `brew` відсутній
  у мінімальному `PATH`, Ariadne знаходить Homebrew у стандартних macOS-шляхах,
  тому реально перезапускаються і Qdrant, і Ollama.
- **Restart завжди намагається відновити сервіси.** `ariadnectl restart` виконує
  start навіть після помилки часткового stop, а потім повертає всі помилки,
  замість того щоб залишити stack вимкненим.
- **Фінальне повідомлення переживає перезапуск tray.** Перевірений результат
  append-only записується до виходу старого процесу; новий tray показує
  завершальне notification і додає marker `delivered`.

### Додано

- **Перевірені service operations.** Tray звіряє Qdrant і Ollama до та після
  Start, Stop або Restart, очікує потрібний стан, перевіряє зміну PID після
  restart і не повідомляє про успіх, якщо collection нездорова.
- **Видимі PID та діагностика операцій.** Tray і `ariadnectl status` показують
  PID сервісів, а лог містить duration, before/after state, command output і
  точну причину невдалої перевірки.

### Змінено

- **Конфліктні дії блокуються на час операції.** Start, Stop, Restart,
  maintenance, update, backup та export не можуть виконуватися одночасно.
- **Platform errors зберігають корисний output.** macOS, Linux і Windows тепер
  повертають stderr/stdout service-команд для діагностики.

## Раніше у v0.8.6

### Виправлено

- **Надійний рестарт tray на macOS.** Керований launchd tray тепер просить
  supervisor запустити одну чисту заміну після завершення старого процесу, тому
  race двох menu-bar процесів більше не залишає систему без іконки.
- **Чесні помилки керування сервісами.** `start`, `stop` і `restart` повертають
  platform error до CLI та tray замість повідомлення про хибний успіх.
- **Claude більше не залишається зі старими integration-файлами.** Installer
  звіряє skill і точні hook paths, matchers та timeouts і оновлює їх за потреби.

### Додано

- **Persistent recall після всіх переходів контексту Claude.** Auto-recall
  працює для `startup`, `resume`, `clear`, `compact` і `fork` та явно нагадує
  одразу зберігати сталі рішення, gotchas і перевірені результати.

### Змінено

- **Схвалення потребує свідомого кліку.** Warning містить лише **Схвалити** та
  **Відхилити**; жодна кнопка не є default чи focused, а Return/Enter і Escape
  не можуть вирішити або закрити запит.
- **Hook installation став update-aware.** Наявні Ariadne hooks оновлюються на
  місці, а сторонні Claude hooks залишаються недоторканими.

## Раніше у v0.8.5

- **macOS warning виходить на передній план.** Нативний діалог активується перед
  показом, тому запит доступу не залишається позаду Codex або Claude Code.
- **Hugging Face Space валідний.** `short_description` вкладається у 60 символів.

## Раніше у v0.8.4

- **Системне вікно підтвердження.** Новий cross-wing або protected-resource
  request одразу відкриває системне warning-вікно, а не покладається лише на
  tray та notification. Воно показує обмежені scope/purpose і кнопки
  **Схвалити**, **Відхилити**, **Пізніше**. Закриття, Escape або «Пізніше» не
  надає жодного доступу; pending request повторно нагадується за хвилину. Черга
  в tray лишається резервним способом і audit view.
- На macOS використовується нативний warning dialog, на Windows — системний
  popup, на Linux — доступний KDialog/Zenity. Append-only decision з’являється
  лише після явного Approve/Deny.

## Раніше у v0.8.3

- **Людське підтвердження міжпроєктної пам’яті.** `all_wings: true` більше не
  запускає пошук одразу: MCP створює pending request, а tray показує active
  wing, purpose та обмежений query. Лише натискання Approve видає 15-хвилинний
  grant, прив’язаний до MCP session, active wing і collection. Після цього
  клієнт повторює запит з `approval_id`.
- **Менша вага інших wings.** Після дозволу зовнішні результати отримують вагу
  0.70 і зазвичай займають не більше двох із п’яти позицій. Відповідь явно
  позначає local/cross-wing origin. Вага застосовується лише після permission
  gate і не замінює його.
- **Окремий одноразовий дозвіл для credentials.** `credential_access` створює
  інший tray request із точними source wing, target wing, назвою/шляхом ресурсу
  та purpose. Grant діє п’ять хвилин і споживається один раз. Ariadne не читає й
  не повертає значення credential.
- **Постійна точна прив’язка за явним рішенням власника.** Для credential-файлу,
  який власник явно надав для повторного використання, `ariadnectl credential
  trust --source-wing ... --target-wing ... --resource ... --purpose ... --yes` один раз
  реєструє exact scope. Наступні виклики для цього tuple не показують popup, але
  кожне використання з purpose потрапляє в append-only аудит. `credential revoke`
  вимикає прив’язку новою подією, не стираючи історію.
- **Append-only аудит дозволів.** Request, tray decision та consumption
  зберігаються окремими незмінними records. `ariadnectl approvals` показує
  pending requests, але не дозволяє схвалити їх через CLI.

## Раніше у v0.8.2

- **Ізоляція проєктів за замовчуванням.** Семантичний `memory_recall` тепер
  вимагає `wing`; пошук по всіх проєктах можливий лише з явним
  `all_wings: true` для запитаного користувачем аудиту. Hooks визначають
  найближчий корінь репозиторію або стабільний slug із `.ariadne-wing`, тому
  вкладена робоча директорія не створює випадково інший namespace.
- **Файлова межа для агентів.** Оновлений спільний skill Codex і Claude Code
  забороняє шукати або позичати `.env`, ключі, IP, endpoints чи конфігурацію з
  сусіднього проєкту лише тому, що той доступний для читання.
- **Детермінований захист секретів.** MCP/store відхиляє приватні ключі,
  credential URI, відомі формати токенів та явні присвоєння секретів. Import і
  capture редагують значення до запису, consolidation повторно перевіряє
  результат, а normal recall не повертає quarantined-записи.
- **Append-only карантин.** `ariadnectl quarantine-secrets` за замовчуванням
  показує лише безпечний dry-run. `--apply` змінює metadata, але зберігає
  оригінальний payload, vector та попередній status для аудиту й відновлення;
  `--apply --reconcile` повертає попередній status після уточнення detector,
  не стираючи історію карантину.

## Раніше у v0.8.1

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
- **Сильніша модель саме для курації.** Capture і consolidation можуть
  використовувати різні локальні моделі через `ARIADNE_CONSOLIDATION_MODEL` та
  `ARIADNE_CONSOLIDATION_JUDGE_MODEL`. У фіксованому тесті на 11 пакетах
  `qwen2.5:7b` відклала п'ять, а `qwen2.5:14b` пройшла всі одинадцять. Тому 14B
  рекомендована для курації, коли вистачає пам'яті, а capture може лишатися на
  меншій моделі.
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

1. MCP-клієнт викликає `memory_save`, `memory_recall`, `credential_access`,
   `memory_move` або `memory_delete`.
2. Ollama з bge-m3 створює багатомовний dense-вектор; BM25 зберігає точні терміни.
3. Qdrant об’єднує dense і sparse результати через RRF; обмежений другий прохід
   враховує явний часовий намір, provenance та завеликий контекст.

Звичайний recall використовує кураторську колекцію `ariadne` і завжди вимагає
`wing`. Сирі архіви сесій лежать окремо в `sessions` і шукаються лише за явним
запитом із `collection: "sessions"` та `wing: "sessions"`. `all_wings: true`
створює окремий tray request і не виконує пошук до людського Approve.

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

Локальний Qdrant, встановлений Ariadne, завжди loopback-only. Для навмисно
віддаленого Qdrant Windows installer приймає `-QdrantHost`,
`-QdrantRestPort`, `-QdrantGrpcPort`, `-QdrantApiKeyFile` і `-QdrantTls`;
у конфіги Codex/Claude, hooks і tray потрапляє лише шлях до key-файлу.
`-AllowInsecureRemoteQdrant` дозволений лише для окремо зашифрованого приватного
тунелю.

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

Міжпроєктний пошук спочатку створює tray request:

```json
{
  "query": "як інші проєкти вирішували повторні спроби",
  "wing": "my-project",
  "all_wings": true,
  "purpose": "знайти придатний для повторного використання підхід"
}
```

Після Approve у tray повторіть ті самі аргументи з отриманим
`"approval_id": "..."`. Grant діє 15 хвилин лише для цієї MCP session, active
wing і collection.

Окреме підтвердження credential:

```json
{
  "source_wing": "service-a",
  "target_wing": "service-b",
  "resource": "deployment credential file",
  "purpose": "одноразовий production deployment"
}
```

Перший `credential_access` лише створить tray request. Після Approve повторний
виклик з `approval_id` споживає grant один раз; значення credential через
Ariadne не передається.

Для явно наданого власником credential-файлу повторні popup можна прибрати
точною локальною прив’язкою:

```bash
ariadnectl credential trust \
  --source-wing shared-credentials \
  --target-wing my-project \
  --resource /secure/path/deploy-token.txt \
  --purpose "publish one release" \
  --yes
```

Відкликання є append-only: повторіть увесь exact scope з `credential revoke --yes`.

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
ariadnectl quarantine-secrets --collections ariadne,sessions
go run ./cmd/eval -cases evaluation/coding-memory.json
```

Для чесного порівняння справжніх BM25 і learned-sparse ранжувань збережіть
впорядковані ID результатів для того самого набору оцінених запитів і запустіть:

```bash
go run ./cmd/eval \
  -retrieval-runs evaluation/retrieval-runs.example.json \
  -baseline bm25
```

Команда рахує macro Recall, MRR, nDCG та різницю nDCG на кожному cutoff;
`-json` повертає машинозчитуваний результат. Файл у репозиторії — лише приклад
формату, не доказ переваги будь-якого методу. Реальний висновок потребує
зафіксованих relevance judgments і запусків на однаковому корпусі, запитах,
фільтрах та cutoff. Eval не звертається до Qdrant, Ollama чи віддаленої моделі й
не змінює памʼять.

Метрики показують:

- **measured saved / net** — економію лише для пам’ятей із відомим розміром джерела;
- **attribution coverage** — частку recall-витрат, яку можна зіставити з джерелом;
- **unattributed** — доставку старих і ручних пам’ятей без вигаданого негативного net;
- **recalls** — кількість фактичних отримань пам’яті.

Повторний recall тієї самої пам’яті в одній клієнтській сесії вдруге не отримує
credit за джерело, але його вартість рахується. `memory_save` має опційний
`source_tokens` для інтеграцій, які знають розмір конкретного стисненого джерела;
capture і consolidate додають provenance автоматично.

Сирі metrics-події залишаються append-only. Схема v3 транзакційно підтримує
денні підсумки для швидкого lifetime-звіту та індекс для 30-денного вікна, не
видаляючи історію.

## Приватність

- Qdrant працює лише на loopback і не має автентифікації за замовчуванням.
- Навмисно віддалений Qdrant працює fail-closed: потрібні API key і TLS.
  Рекомендований клієнтський параметр — `ARIADNE_QDRANT_API_KEY_FILE` із правами
  `0600`; також підтримується process-only `ARIADNE_QDRANT_API_KEY`.
  `ARIADNE_QDRANT_TLS=1` вмикає TLS для gRPC, а REST URL має використовувати
  `https://`. Інсталер переносить у MCP-конфіг лише шлях до key-файлу, не саме
  значення. `ARIADNE_QDRANT_ALLOW_INSECURE_REMOTE=1` — явний виняток лише для
  окремо зашифрованого приватного тунелю, не для звичайної LAN.
- Пам’ять зберігається у plaintext payload, тому секрети записувати не можна.
- Нові записи з детерміновано виявленими credentials відхиляються; import і
  hooks редагують значення, а старі збіги можна append-only ізолювати командою
  `ariadnectl quarantine-secrets --apply` після перевірки dry-run.
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

Згенеровані артефакти сайту можна прибрати з working tree без втрати даних:
`make clean` переносить build output у `~/.ariadne/archive/site/` разом із
manifest для відновлення, а `make clean-all` додатково переносить
`site/node_modules`.
