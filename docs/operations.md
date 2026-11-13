# Развёртывание, PostgreSQL и восстановление

Самостоятельный Go-бинарник теперь также работает с одним YAML-файлом. Полный перенос PostgreSQL ↔ YAML, секреты, блокировки и dry-run описаны в [yaml-storage.md](yaml-storage.md). Это отдельные команды `qag data export/import`; прежние контейнерные `export-config` и `pg_dump/pg_restore` сохраняют своё назначение.

## Состояние установки

`docker compose up --build -d --wait` запускает qag и один PostgreSQL. Одноразовые init и db-setup последовательно готовят конфигурацию/PKI и ограниченную demo-роль. Готовность qag проверяет основную БД, demo-пул и работающий gRPC. `docker compose ps -a` показывает подготовительные контейнеры с Exit 0 и оба постоянных сервиса healthy.

| Том | Содержимое | Доступ |
|---|---|---|
| `qag_postgres-data` | Физическая PostgreSQL 18, БД qag | PostgreSQL RW, init RO |
| `qag_runtime-config` | installation.json: мастер-ключ и подключения | init/import RW, qag и db-setup RO |
| `qag_private-pki` | CA с приватным ключом, серверная пара | Только подготовка и экспорт |
| `qag_postgres-config` | Пароль PostgreSQL, публичный CA, серверная пара, HBA | init RW, PostgreSQL RO |

Документ конфигурации записывается атомарно, маркер завершения — последним. Повторная подготовка проверяет согласованность и сохраняет ключи. Частичное состояние, повреждённая/истекающая PKI и БД без исходной конфигурации вызывают отказ; автоматической ротации нет. PostgreSQL не получает мастер-ключ qag и приватный ключ CA. Серверная пара копируется в его runtime-каталог с владельцем postgres и режимом ключа 0600.

`docker compose down` и `make down` сохраняют тома. Не добавляйте `--volumes` к обычной остановке. Потеря мастер-ключа делает сохранённые секреты недоступными.

HTTP публикуется на `127.0.0.1:8080`; `QAG_HTTP_PORT` меняет только порт хоста. В контейнере listener `0.0.0.0:8080`, `QAG_PUBLIC_ORIGIN` задаёт разрешённый локальный адрес браузера. Host/Origin/CSRF/CSP и Sec-Fetch-Site проверяются; forwarded-заголовки список разрешений не расширяют. Нативный режим сохраняет loopback-only listener. Demo gRPC слушает `127.0.0.1:9091` внутри qag и не публикуется.

## Переход с прежней установки

Завершите/отмените активные прогоны и остановите нативный qag. Оставьте PostgreSQL работающей. До импорта сохраните исходные `.local/dev.env`, `.local/pki` и закрытый dump. Например, выбрав новую папку `my-backup` в защищённом каталоге:

```console
docker exec qag-postgres-1 pg_dump -U qag -d qag --schema=qag --schema=qag_demo --format=custom --no-owner --no-acl -f /tmp/qag-before-container.dump
docker cp qag-postgres-1:/tmp/qag-before-container.dump ./my-backup/qag.dump
```

Заранее создайте папку и скопируйте в неё исходные env/PKI средствами файловой системы, ограничив доступ своей учётной записью. Не направляйте бинарный dump через текстовое перенаправление PowerShell. Затем:

```console
docker compose --profile tools run --build --rm --no-deps import-local
docker compose up --build -d --wait
```

Импорт монтирует `.local` только для чтения и не создаёт отсутствующий исходный каталог. Поддерживаются локальные PostgreSQL URI на порту 15432 с БД qag, согласованным паролем и `sslmode=disable`. В копии меняется только сервер на `postgres:5432`; имя динамической demo-роли, пароль и мастер-ключ сохраняются. Внешние DSN, дополнительные TLS-параметры и конфликт существующего состояния отклоняются без частичной замены. Для такого подключения нужна отдельная явная миграция с подходящими сертификатами, автоматического понижения TLS нет.

Сохраняются project name qag, сервис postgres и прежний `qag_postgres-data`. Пользовательские ресурсы и ciphertext не переписываются. Системные подключения получают новую версию при смене адреса; ID встроенного кейса и старая история остаются прежними. Применённые SQL-миграции не меняются.

Для отката остановите контейнер qag и запустите прежний бинарник с исходными env/PKI, оставив PostgreSQL. Если после перехода менялись данные, используйте актуальный согласованный backup.

## Резервная копия Docker без Go/Node/PowerShell

Завершите активные прогоны и остановите qag. PostgreSQL оставьте работающей. Создайте новую защищённую папку `my-backup`. Следующие команды создают закрытые файлы в служебном контейнере, затем копируют их на хост:

```console
docker compose stop qag
docker compose --profile tools run --no-deps --name qag-backup-config export-config
docker cp qag-backup-config:/export/. ./my-backup
docker rm qag-backup-config
docker exec qag-postgres-1 pg_dump -U qag -d qag --schema=qag --schema=qag_demo --format=custom --no-owner --no-acl -f /tmp/qag-backup.dump
docker cp qag-postgres-1:/tmp/qag-backup.dump ./my-backup/qag.dump
docker exec qag-postgres-1 rm -f /tmp/qag-backup.dump
docker compose up -d --wait
```

Выполняйте следующую команду только после успешного завершения предыдущей. Выбирайте новую папку и свободное имя служебного контейнера; `docker cp` само по себе не защищает от перезаписи. Не запускайте повторно копирование в ту же папку. На Linux ограничьте права каталога 0700 и файлов 0600; на Windows используйте приватный ACL.

Копия содержит `installation.json`, `dev.env`, `pki/` и `qag.dump`. Dump включает только qag/qag_demo: сценарии, окружения, ciphertext, изображения, варианты, очереди и историю. Роли PostgreSQL в dump не входят; их исходные реквизиты находятся в отдельной конфигурации. Храните приватную конфигурацию отдельно от дампа с ограниченным доступом.

В Windows есть эквивалент со строгим отказом перезаписи и приватным ACL:

```powershell
.\scripts\backup.ps1 -Directory .local/backups/my-backup
```

Скрипт использует фактическую конфигурацию Docker, а для прежней установки сначала согласует исходную env/PKI. Дополнительно сохраняется `qag-secret-key.txt`; ключ не печатается. Скрипт не останавливает приложение автоматически: согласованную точку выбирает оператор.

## Восстановление

Для полного восстановления остановите приложение и используйте отдельную чистую установку с той же версией PostgreSQL и исходной приватной конфигурацией. Не направляйте pg_restore на заполненные рабочие схемы.

В новой установке используйте init с RW-томами конфигурации и временным shell вместо обычного entrypoint:

```console
docker compose run --build -d --no-deps --name qag-restore-state --entrypoint /bin/sh init -c "sleep 600"
docker cp ./my-backup/. qag-restore-state:/source
docker exec qag-restore-state qag-setup -operation restore -source /source
docker stop qag-restore-state
docker rm qag-restore-state
docker compose up -d --wait postgres
docker cp ./my-backup/qag.dump qag-postgres-1:/tmp/qag-restore.dump
docker exec qag-postgres-1 pg_restore -U qag -d qag --no-owner --no-acl --exit-on-error /tmp/qag-restore.dump
docker exec qag-postgres-1 rm -f /tmp/qag-restore.dump
docker compose up --build -d --wait
```

Исходный JSON и CA восстанавливаются до init/PostgreSQL; чужая конфигурация не заменяется. Provisioner создаёт demo-роль с прежними реквизитами, bootstrap назначает права. Проверьте каталог, историю, эталоны, чтение секретов и встроенный пример. Прерванные операции автоматически не повторяются. Для внешней БД используйте pg_dump/pg_restore её основной версии и реальный мастер-ключ.

Автоматический `TestBackupRestoreCatalogHistoryAndSecret` проверяет реальный dump/restore **в уникальных схемах той же qag**, без второго сервера/БД. Fixture временно переименовывает только свои зарегистрированные схемы в _source, восстанавливает исходные имена и назначает ограниченного владельца. Проверяются ciphertext, история, эталоны, варианты, builtin ID и checksum миграций.

## Нативная разработка и общие проверки

Для возврата к Windows-разработке остановите `docker compose stop qag` и выполните:

```powershell
.\scripts\init-local.ps1
.\scripts\database.ps1 -IncludeTests
.\scripts\build.ps1
.\scripts\run.ps1
```

Init экспортирует те же реквизиты/PKI в .local; существующий согласованный файл сохраняется, конфликт вызывает отказ. Резервный закрытый экспорт остаётся в `.local/native-export-<id>`. Экспорт отдельно: `scripts/export-config.ps1 -Directory <новая папка>`. Не заменяйте исходный ключ случайным. Native build/run требуют Go/Node, Docker-путь — только Docker/Compose.

Для нативной работы с внешней PostgreSQL задайте оба URL и фактический мастер-ключ в окружении Go-процесса либо в приватном `.local/dev.env`, который читает `run.ps1`. Локальные `init-local.ps1`/`database.ps1` для внешней БД не применяются. Администратор предварительно подготавливает отдельную demo-роль с теми же URL в окружении:

```powershell
go build -o .tools/qag-demo-provision.exe ./cmd/demo-setup
if ($LASTEXITCODE -ne 0) { throw 'Demo provisioner build failed' }
& .\.tools\qag-demo-provision.exe
if ($LASTEXITCODE -ne 0) { throw 'Demo role preparation failed' }
```

Оба подключения должны указывать на один сервер/порт/БД и разных пользователей. Demo DSN поддерживает `sslmode=verify-full` (также default), `sslrootcert`, `sslcert`, `sslkey`; PEM-пути читаются из доверенной конфигурации. Обычному qag нужны права миграций в своих схемах, а demo-reader получает только USAGE/SELECT в demo-схеме и не имеет доступа к основной схеме. Повышенные права demo-роли и чужие роли отклоняются.

Рабочая БД и тесты используют qag на 15432: рабочие схемы qag/qag_demo, тестовые — уникальные qag_test_<32 hex>/<app>_demo/<app>_target. Fixture создаёт отдельные NOSUPERUSER/NOCREATEDB/NOCREATEROLE-роли и хранит ID/nonce/OID/owner в qag_test.manifest. Очистка проверяет принадлежность; нельзя удалять схемы по одному префиксу или завершать общие backend-подключения. Группа qag_test_mtls требует TLS cert-auth, рабочий доступ сохраняет SCRAM.

`scripts/test-integration.ps1` проверяет изоляцию, протоколы, backup/restore и миграции. `scripts/test-container.ps1` использует тот же fixture для Linux-контейнера, порт 18088 и Chromium хоста; нужны Go, Node, web dependencies и установленный Playwright Chromium. `scripts/verify.ps1` сохраняет полный нативный Windows E2E, включая clipboard. Второй тестовый PostgreSQL не создаётся; прежний qag_postgres-test-data автоматически не удаляется.

## Ресурсы, экспорт и диагностика

В контейнере localhost указывает на qag. Для сервиса хоста используйте host.docker.internal; Compose добавляет Linux host-gateway. Сервис должен принимать соединения с Docker, его TLS-сертификат — соответствовать выбранному адресу. Для соседнего контейнера используйте DNS сервиса и его внутренний порт. Пользовательские подключения автоматически не переписываются.

Встроенный HTTP обращается к loopback:8080 внутри qag, gRPC — loopback:9091, SQL — postgres:5432 под отдельной ролью. Все три интерфейса читают qag_demo.items. Bootstrap повторяем и не создаёт прогонов.

PDF, текст и Markdown формируются браузером. Linux-backend сообщает clipboardNative=false, поэтому оформленный текст копируется browser HTML clipboard с PNG. Это зависит от разрешений и возможностей браузера; отказ показывается явно. Native RTF/HTML/Unicode Windows clipboard доступен только нативному Windows-backend.

| Симптом | Действие |
|---|---|
| Docker недоступен | Дождитесь Linux Engine; Make возвращает ошибку Docker |
| init завершился с ошибкой | Просмотрите `docker compose logs init`; восстановите исходный JSON/PKI или выполните явный import-local |
| БД существует, конфигурации нет | Импортируйте/восстановите исходный ключ и реквизиты; не создавайте замену |
| Конфликт импорта | Сверьте источник с сохранённой конфигурацией; перезапись запрещена |
| Ошибка demo-роли | Проверьте db-setup; чужие роли и повышенные права не перенастраиваются |
| Порт занят | Выберите QAG_HTTP_PORT и повторите Compose up |
| Схема несовместима | Используйте подходящую версию; применённые миграции не редактируйте |
| Другой экземпляр использует БД | Оставьте один экземпляр в рабочем namespace |
| Секрет не расшифровывается | Восстановите исходный мастер-ключ |
| interrupted/отмена/тайм-аут | Проверьте внешний эффект до новой явной попытки |

При обновлении сохраняйте dump и конфигурацию до пересборки. Сейчас девять миграций: 009 добавляет политику закрепления прогона и выбор шага очереди. Миграции 001–008 и исторические JSON остаются прежними. Повторный запуск миграций безопасен; для отката на более старую схему восстанавливайте согласованную копию, не удаляйте строки миграций вручную. Новый YAML записывается в v2, старый v1 читается без перезаписи до настоящего изменения; подробности — [yaml-storage.md](yaml-storage.md).
