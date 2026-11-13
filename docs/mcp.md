# MCP API qag

qag предоставляет 45 типизированных инструмента через stateless Streamable HTTP на точном пути `/mcp`. Используется официальный Go SDK v1.8.0, поддерживаются `2026-07-28` (discovery) и `2025-11-25` (initialize). Все операции используют существующие Engine, репозитории, миграции и shutdown. Подключение клиента не запускает второй экземпляр приложения и не повторяет вызовы.

## Подключение

MCP включён при каждом запуске qag. В клиенте укажите **`http://127.0.0.1:8080/mcp`**, а при опубликованном порте 18088 — `http://127.0.0.1:18088/mcp`. Авторизация, токен и браузерная сессия не требуются. Все 45 инструмента и выданные артефакты доступны без отдельной настройки доступа.

На точном пути `/mcp` не применяются проверки Host, Origin, cross-site и SDK localhost protection. CORS разрешает любой Origin; OPTIONS принимает запрошенные заголовки и возвращает 204. Браузерный API `/api` использует прежние Host/Origin/CSRF-проверки. Нативный listener и публикация порта Compose настраиваются как раньше — см. [README](../README.md#mcp).

Прежний `QAG_MCP_TOKEN` больше не используется. Его отсутствие, пустое или старое значение в среде/приватном файле не отключает MCP и не влияет на запуск. Дополнительного listener, контейнера, OAuth или формы входа нет.

SDK отправляет RPC через POST с `Content-Type: application/json`, `Accept: application/json, text/event-stream` и согласованной версией протокола. GET/SSE и DELETE для stateless-сессий возвращают протокольный 405. Ответы имеют `Cache-Control: no-store`; современный MCP дополнительно сообщает `cacheScope: private, ttlMs: 0`.

## Контракты

`initialize.instructions` направляет агента к постоянному Markdown-ресурсу **`qag://guide/workflows`**. Он доступен через `resources/list` и `resources/read`, встроен в Go-бинарник и не имеет TTL артефактов. Полный текст: [руководство агента](../internal/mcpserver/guide.md). Начните с `qag_status` и discovery реальных ID через `qag_demo_get`: встроенный кейс является эталоном структуры и доступен только для чтения/запуска. Отрицательные HTTP 404/gRPC NOT_FOUND в нём ожидаемы; YAML demo SQL возвращает `demo_sql_unavailable` и не должен объявляться успешным.

Источники истины: [реестр и схемы](../api/mcp-tools.json), [Go DTO](../internal/mcpserver/inputs.go), [OpenAPI общих моделей](../api/openapi.json). `tools/list` выдаёт `inputSchema`, `outputSchema`, описание и annotations каждого инструмента. Неизвестные поля, включая вложенные, отклоняются. UUID передаются строками; `revision` — положительным целым; `limit` — 1–100 (по умолчанию 50), `cursor` возвращается как `nextCursor`. Списки каталога сохраняют алфавитную сортировку и правила архива, история идёт от новых прогонов к старым.

Успешный результат содержит `structuredContent.data`; тот же envelope сериализован в текстовом content. Результат крупнее 1 MiB содержит `artifact` и `summary` вместо `data` — скачайте JSON по частям и разбирайте исходные байты. Ошибки предметной области имеют `isError: true` и `error: {code, message, fieldErrors?, requestId}` без успешного `data`. Ошибки схемы/параметров — безопасный JSON-RPC `-32602`; незнакомые методы/инструменты и transport-ошибки обрабатываются SDK. Основные коды: `invalid`, `not_found`, `conflict`, `read_only`, `storage_unavailable`, `cancelled`, `busy`, `artifact_expired`, `artifact_too_large`, `result_too_large`, `internal_error`. Сырой PostgreSQL/TLS-текст и переданные секреты не включаются в сообщения; аудит содержит только инструмент, код, requestId и длительность.

`id` в однопредметных get/update/finish/cancel/save/delete — ID соответствующего объекта. Там, где объектов несколько, используются явные `runId`, `testCaseId`, `environmentId`, `blockId`, `variantId`, `imageId`, `stepId`.

## Каталог инструментов

Знак `?` обозначает необязательный параметр; состав вложенных `config`, `steps`, `request` и результата указан в JSON Schema. Все 45 инструмента:

| Инструмент | Параметры | Результат / действие |
|---|---|---|
| `qag_status` | `{}` | Readiness, протоколы, возможности clipboard и лимиты; без конфигурации/credentials |
| `qag_demo_get` | `{}` | Стабильные ID встроенного кейса, окружения и трёх ресурсов |
| `qag_folders_list` | `archived?, limit?, cursor?` | Страница папок выбранной вкладки |
| `qag_folder_create` | `name` | Новая папка |
| `qag_folder_update` | `id, name?, archived?` | Переименование, архивирование или восстановление |
| `qag_test_cases_list` | `folderId, limit?, cursor?` | Страница кейсов, архивные идут вниз |
| `qag_test_case_create` | `folderId, name?` | Черновик; пустое имя назначается автоматически |
| `qag_test_case_export` | `id, revision, format:"jira-wiki", includeExpected?` | Артефакт сценария для комментария Jira, без прогона |
| `qag_test_case_get` | `id` | Полный кейс и validationIssues |
| `qag_test_case_save` | `id, revision, name?, steps?` | Полная версия шагов/блоков и ожиданий; допускает неполный черновик |
| `qag_test_case_update` | `id, name?, archived?` | Переименование, архивирование или восстановление |
| `qag_image_upload` | `testCaseId, filename, dataBase64` | Метаданные эталона PNG/JPEG/WebP, до 10 MiB |
| `qag_image_get` | `testCaseId, imageId` | Метаданные и artifact исходных байтов |
| `qag_environments_list` | `limit?, cursor?` | Страница окружений |
| `qag_environment_get` | `id` | Имя/описание, открытые переменные, имена секретов |
| `qag_environment_create` | `name, description?` | Пустое окружение без переменных |
| `qag_environment_update` | `id, revision, name, description?` | Метаданные с сохранением переменных/секретов |
| `qag_variables_set` | `id, revision, variables, secrets?` | Полная замена открытых переменных, изменение секретов |
| `qag_resources_list` | `environmentId, limit?, cursor?` | Страница ресурсов |
| `qag_resource_get` | `id` | Публичный ресурс, версия, TLS, proto, источники заголовков |
| `qag_resource_create` | `environmentId, name, kind, config, secrets?` | Новый ресурс `http`/`grpc`/`postgresql` |
| `qag_resource_update` | `id, revision, name, kind, config, secrets?` | Полная версия конфигурации; окружение не меняется |
| `qag_proto_bundle_upload` | `id, revision, files` | Версия gRPC-ресурса с map относительных имён к текстам proto |
| `qag_runs_list` | `testCaseId, retained?, active?, limit?, cursor?` | Страница истории/активных прогонов |
| `qag_run_start` | `testCaseId, environmentId, revision, executeAll?, historyPolicy?, idempotencyKey` | Устойчивый ID и снимок прогона; необязательная очередь |
| `qag_run_get` | `id` | Снимок, статусы, ресурсы, очередь, варианты/версии и попытки |
| `qag_run_step_execute` | `runId, stepId, idempotencyKey` | Очередь исходных блоков выбранного шага активного прогона |
| `qag_run_step_update` | `runId, stepId, revision, status, comment` | Ручной статус `not_run`/`passed`/`failed`, комментарий |
| `qag_run_steps_pass_all` | `runId, steps: [{stepId, revision}]` | Атомарно passed для полного набора шагов, комментарии сохраняются |
| `qag_run_finish` | `id` | Терминальный итог и автоматические причины сохранения |
| `qag_run_cancel` | `id` | Явная отмена прогона, очереди, источников и попыток |
| `qag_run_save` | `id` | Ручное закрепление завершённого прогона |
| `qag_run_delete` | `id` | `{deleted:true}` для допустимого несохранённого терминального прогона |
| `qag_batch_start` | `runId, idempotencyKey` | Последовательная очередь всех исходных блоков |
| `qag_editor_attempt_start` | `testCaseId, blockId, environmentId, revision, idempotencyKey` | Пробная попытка без создания прогона |
| `qag_run_attempt_start` | `runId, blockId, idempotencyKey` | Исходная попытка с официальным сравнением |
| `qag_attempt_get` | `id` | Сохранённое состояние, фактический ответ и сравнение |
| `qag_attempt_cancel` | `id` | Явная отмена одной попытки |
| `qag_variant_create` | `runId, blockId, name?` | Исследовательская вкладка без ожидания |
| `qag_variant_update` | `runId, variantId, revision, name?, request` | Новая request-only версия |
| `qag_variant_attempt_start` | `runId, variantId, revision, idempotencyKey` | Фактический ответ версии без сравнения |
| `qag_report_prepare` | `runId, includeExpected?` | Согласованный report с snapshotId/hash, ожидания включены по умолчанию |
| `qag_report_export` | `runId, snapshotId, hash, includeExpected, format` | Files: PDF/text/markdown или rich HTML/RTF/text |
| `qag_report_copy` | `runId, snapshotId, hash, includeExpected, format` | copied, files, возможные code/message; только text/markdown/rich |
| `qag_artifact_read` | `id, offset?, length?` | artifact, offset, length, dataBase64, eof |

## Редактирование и выполнение

В `config.variables` передаётся map определений: `{"LOCAL":{"mode":"literal","value":"text"},"AUTH_VALUE":{"mode":"http","source":{...}}}`. `source` имеет тот же полный HTTP-контракт, что источник заголовка, включая JSON Pointer и `fetchMode: per_attempt|per_run`. Полученное скалярное значение используется как `${AUTH_VALUE}` в HTTP, gRPC и типизированных SQL-параметрах этого ресурса. HTTP-источники используют только статические переменные и сохранённые секреты; цепочки HTTP→HTTP отклоняются до сети. Полученные значения и их экранированные формы маскируются; в `variableSources` выдаются только имя, режим, состояние, HTTP-статус и длительность. Определения сохраняются в снимке ресурса, значения и кеш не переносятся в YAML/историю.

Новые пустые имена в открытых/секретных переменных окружения и literal/http-переменных ресурса пропускаются, включая строки из пробелов. Остальные имена должны быть уникальны в эффективном контексте. Для удаления существующего секрета используйте `clear` с его настоящим именем. `config.headers` задаёт общие HTTP-заголовки/gRPC metadata; заголовок вызова перекрывает общий без учёта регистра и без запуска перекрытого источника. MCP не читает файлы сертификатов на сервере: клиент передаёт UTF-8 PEM в `tls.ca`, `tls.certificate` и `secrets.replace.PRIVATE_KEY` (до 1 MiB на файл).

Полный запуск: `qag_run_start` с `executeAll:true`. Для отдельного шага сначала создайте прогон без очереди, затем вызовите `qag_run_step_execute` с `runId`, `stepId` из снимка и новым UUID команды. Возвращается устойчивая очередь; читайте прогресс через `qag_run_get`. Выполняются только исходные блоки выбранного шага по порядку, после ошибок очередь продолжается. Ручной шаг создаёт завершённую пустую очередь без фиктивной попытки. Другие шаги и ручные статусы не меняются; прогон остаётся открытым. Параллельная активная очередь/попытка блокирует новый запуск; явные отмена, восстановление после перезапуска и защита от повтора действуют как у полной очереди.

`historyPolicy` задаётся при `qag_run_start`: `auto` (по умолчанию) сохраняет прежние first_run/first_success; `retain` добавляет manual при любом завершении, отмене или interrupted; `unretained` не закрепляет прогон автоматически и не занимает первые слоты. Все режимы сохраняют ID и попытки в PostgreSQL/YAML. Такой прогон доступен по ID и в списке `retained:false`, а последующий `qag_run_save` добавляет manual. Политика неизменна и входит в idempotency hash; отсутствие поля и явный `auto` эквивалентны.

`case_save` заменяет весь список `steps`. Сохраняйте стабильные UUID существующих шагов/блоков; удаление и перестановка задаются самим списком. У шага есть необязательный `title`, `description`, `blocks`, ручное `expected` с `text` и `images:[{imageId,caption?}]`; у блока — `kind`, `resource` (имя из выбранного окружения), `timeoutSeconds`, соответствующий `http`/`grpc`/`sql` и `expected`. Сначала загрузите эталон в тот же кейс, затем сохраните ссылку. Пустые черновики разрешены, проблемы возвращаются в `validationIssues`; запуск требует готового сценария. Системный пример и его окружение/ресурсы доступны только для чтения и запуска.

`secrets: {replace?: {NAME: value}, clear?: [NAME]}`: отсутствие имени сохраняет прежний секрет, `replace` заменяет его (включая пустую строку), `clear` удаляет. Ответ содержит только имена секретов. В `variables_set` передавайте полный набор открытых переменных. Не записывайте пароли/ключи в открытые поля.

`config` использует `baseUrl` для HTTP, `address` для gRPC/PostgreSQL, `database`/`username` для PostgreSQL; `headers` — массив, `tls:{mode:"plaintext"|"tls"|"mtls",ca?,certificate?,serverName?}`. Закрытый ключ хранится как `secrets.replace.PRIVATE_KEY`, пароль PostgreSQL — `PASSWORD`. В mTLS передавайте согласованную пару `tls.certificate` и `PRIVATE_KEY`. Proto bundle содержит тексты и относительные импорты; абсолютные/выходящие пути и streaming не поддерживаются. Файлы хоста по переданным путям не читаются.

Заголовки ресурса, HTTP-вызова или gRPC metadata могут использовать `source` с `method,url,query,headers,bodyMode,body,tls,jsonPointer,prefix,fetchMode` и необязательным `privateKeySecret`. У такого заголовка задаются стабильный UUID `id`, `name`, `source`; строковый `value` не используется. Режим `per_attempt` обновляет значение для каждой попытки, `per_run` повторно использует его только внутри своего прогона и неизменной версии источника. Значение выбирается JSON Pointer (например `/tokens/0/value`); переменные `${NAME}` подставляются из окружения/ресурса. Полученные токены маскируются в ответах, истории и отчётах.

Сохранение версий проверяет `revision`; после `conflict` прочитайте актуальный объект и предложите осознанное обновление. Для pass-all нужен полный набор `{stepId,revision}`; устаревший, чужой или пропущенный шаг отклоняет всю команду. Команда не выполняет сеть и не меняет исходные результаты сравнения.

Все запускающие команды требуют отдельный UUID `idempotencyKey`. Сохраните ключ **до** отправки и при потере ответа повторите тот же payload с тем же ключом: получите прежний ID без повторного внешнего эффекта. Изменённый payload с тем же ключом даёт conflict. MCP JSON-RPC id не заменяет этот ключ. Новая попытка — новый ключ.

Отключение MCP-клиента или отмена ожидания не отменяет уже зарегистрированную Engine-задачу. Читайте её по ID либо повторите команду с прежним ключом. Для остановки используйте явные attempt/run cancel. Выполненный внешний эффект не откатывается. После перезапуска приложение отмечает прерванную работу, не повторяя внешние действия.

Очередь выполняет исходные блоки по порядку, продолжает после неудачи, пропускает ручные шаги и варианты. Второй активный запуск блокируется; ручные оценки выставляются отдельно. Вариант сохраняет родительские kind/resource; `request` допускает только `timeoutSeconds`, `http`, `grpc`, `sql`. Поля `expected`, `kind`, `resource` во входе варианта отклоняются. Предыдущие версии/попытки остаются в истории; фактические ошибки HTTP/gRPC и SQL rows/exec доступны без сравнения и не определяют официальный итог.

## HTTP-переменные окружения и form-urlencoded

`qag_variables_set` принимает отдельный объект `httpVariables` с HTTP-определениями. Поле `variables` остаётся полным набором обычных строк. Если `httpVariables` пропущено, определения сохраняются; `{}` явно удаляет их, `null` отклоняется. Строки, источники и изменения секретов фиксируются атомарно по одной revision. Пустые новые имена пропускаются до проверки вложенных полей.

Получите реальный environmentId и revision через `qag_environment_get`. Пример аргументов (UUID условный; CLIENT_SECRET предварительно задаётся через `secrets.replace`):

```json
{
  "id": "00000000-0000-4000-8000-000000000001",
  "revision": 1,
  "variables": {"CLIENT_ID": "qag"},
  "httpVariables": {
    "ACCESS_TOKEN": {
      "method": "POST", "url": "https://auth.example.test/token",
      "query": [], "headers": [], "bodyMode": "form", "body": "",
      "form": [
        {"name": "grant_type", "value": "client_credentials"},
        {"name": "client_id", "value": "${CLIENT_ID}"},
        {"name": "client_secret", "value": "${CLIENT_SECRET}"}
      ],
      "tls": {"mode": "tls"}, "jsonPointer": "/access_token",
      "fetchMode": "per_run"
    }
  }
}
```

Пары формы заменяют обычное `curl --data-urlencode name=value`: передавайте исходные строки, кодирование выполняет qag после подстановки. Повторные имена и пустые значения сохраняются. Content-Type выставляется автоматически, несовместимый тип или одновременные form и текстовое body отклоняются. Формат обычных блоков HTTP остаётся прежним; `form` относится к источникам значений.

Источник окружения видит только его статические переменные/секреты; `per_run` переиспользуется между ресурсами одного прогона. Источник в `config.variables` видит также контекст своего ресурса и имеет отдельный кеш версии ресурса. HTTP-зависимости между источниками запрещены. Для общего Authorization используйте `{"name":"Authorization","value":"Bearer ${ACCESS_TOKEN}"}` в `config.headers`.

В `headers[].source` и в источниках переменных доступны `prefix` и `suffix`: результат равен тексту до, извлечённому скаляру и тексту после. Например, `prefix="Bearer "`, `suffix=""` сохраняет пробел перед токеном; `prefix="Token ["`, `suffix="]"` обрамляет значение. У отдельного источника заголовка также нужен UUID `id` самого заголовка. Полученные значения маскируются и не возвращаются при чтении настроек.

`qag_resource_update` сохраняет весь `config` и изменения `secrets` одной версией: адрес, общие заголовки, все источники, локальные переменные, TLS и proto. При ошибке любого раздела или revision вся команда отклоняется. Чтение/сохранение конфигурации не выполняет источники; новый прогон использует новую версию, уже начатый сохраняет старую.

## Отчёты и артефакты

`qag_test_case_export` принимает `id`, актуальную `revision`, `format:"jira-wiki"` и необязательный `includeExpected` (default true). Он экспортирует только определение сохранённого сценария, включая неполный черновик и read-only пример: без прогона, фактических ответов и публикации в Jira. Возвращает UTF-8 `.jira.txt` артефакт с обычными chunk/TTL/SHA-256 правилами. Это тот же текст, что показывает UI. Шаблоны не вычисляются, чувствительные заголовки скрыты; изображения перечисляются по имени/подписи с инструкцией приложить их вручную. `qag_report_export` остаётся экспортом результата прогона.

`report_prepare` не выполняет сеть и не изменяет историю. UI и MCP используют общий кэш срезов: подготовленный в UI snapshot можно экспортировать через MCP. Для каждого экспорта передавайте все четыре поля ссылки: `runId,snapshotId,hash,includeExpected`. Поздняя попытка или новое редактирование не меняет старый срез; истёкший/чужой срез даёт conflict, автоматической пересборки нет. Байты эталонов восстанавливаются по приватному ownerCaseID, ID и SHA; приватный ID не сериализуется в ответ. `includeExpected:false` исключает текстовые/табличные/графические ожидания и сравнения, сохраняя фактические ответы. Варианты не включают ожидания при любом переключателе, включая старые данные.

Форматы: `pdf` — готовый PDF с встроенными Noto Sans/Mono, кириллицей, изображениями, таблицами и правыми ручными отметками; `text` — Unicode; `markdown` — безопасные заголовки/код и data-изображения; `rich` — три самостоятельных HTML/RTF/text файла с PNG вызовов. PDF создаётся pure Go (gopdf v0.38.3), PNG — x/image; Node.js/Chromium в контейнере не нужны. Лицензии шрифтов: [OFL](../web/public/fonts/OFL.txt). Неподдерживаемый шрифтом символ вызывает явный отказ вместо потери текста.

`report_export` только готовит файлы. `report_copy` дополнительно записывает native Windows clipboard: UnicodeText для text/markdown, UnicodeText+HTML+RTF для rich. В Linux/контейнере возвращаются `copied:false, code:"clipboard_unavailable", files:[...]`; clipboard Docker-хоста недоступен, скопируйте содержимое средствами MCP-клиента. При отказе Windows clipboard возвращается `copied:false, code:"clipboard_error"` и подготовленные файлы. Никогда не сообщайте пользователю об успешном копировании только по наличию файлов.

Descriptor артефакта: `id,uri,name,mimeType,size,sha256,expiresAt`. URI имеет вид `qag://artifacts/<UUID>`. `resources/list` перечисляет только выданные текущим сервером артефакты, `resources/read` читает целиком только до 1 MiB. Произвольные URI/path не поддерживаются. Для большого файла вызывайте `qag_artifact_read` с `id`, byte `offset` и `length` до 262144 (по умолчанию столько же). Декодируйте `dataBase64`, склейте байты до `eof:true`, проверьте `size` и SHA-256 и только потом декодируйте UTF-8/JSON. Нельзя декодировать каждый кусок в UTF-8 отдельно: символ может пересекать границу. JSON bodies и SQL typed values сохраняют точные числа строками; избегайте преобразования decimal в float.

Пределы: тело MCP 32 MiB; эталон до 10 MiB; inline JSON 1 MiB; файл до 64 MiB; до 32 файлов / 256 MiB в кэше артефактов; TTL 10 минут с возможным более ранним вытеснением. Rich-пакет публикуется атомарно. Подготовленных report-срезов до 16 / 64 MiB, TTL 10 минут; байты изображений в нём не дублируются. До двух одновременных серверных рендеров, 60 секунд на задание; rich до 512 частей / 24 MiB PNG, PDF до 5000 страниц, до 250000 строк на текстовый блок при разметке PDF/PNG. Превышение даёт явную ошибку без частичного успешного файла. После перезапуска артефакты и кэш отчётов исчезают, сохранённая история остаётся в PostgreSQL.

## Пример и проверка

[Пример на официальном SDK](../cmd/mcp-example/main.go) содержит подключение без токена, получение demo, создание executeAll-прогона с UUID, опрос, ручной pass-all, finish, prepare, export и чтение частей с SHA. Пример выполняет ручную оценку сам — для пользовательских кейсов агент должен получать её от проверяющего.

```powershell
go run ./cmd/mcp-example -url http://127.0.0.1:8080/mcp -format pdf -out .local/mcp-report
go run ./cmd/mcp-example -url http://127.0.0.1:18088/mcp -protocol 2025-11-25 -format rich -out .local/mcp-rich
.\scripts\test-mcp.ps1
.\scripts\test-container.ps1
```

Проверки используют уникальные схемы/ограниченные роли существующей PostgreSQL `qag`, анонимное MCP-подключение и собственные локальные цели. Нативная Windows-проверка сохраняет и восстанавливает прежний clipboard и проверяет вставку в Word. Контейнерная проверка подтверждает UID 10001, отсутствие Node/браузера, экспорт и сохранность данных после SIGTERM/recreate. Фактические результаты — [verification.md](verification.md).

MCP не управляет DOM, темой, фокусом, scroll, localStorage и открытыми вкладками браузера; серверный экспорт и явный clipboard предоставляют соответствующие предметные действия UI. Универсального прокси для HTTP/SQL/файлов вне сохранённых ресурсов и сценариев нет.
