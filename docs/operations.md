# PostgreSQL, ключ и восстановление

Compose создаёт постоянные тома `qag_postgres-data` и `qag_postgres-test-data`. Рабочая БД — `qag` на `127.0.0.1:15432`; тестовая — `qag_test` на `127.0.0.1:15433`. SQL-ресурсы подключаются отдельно. Для обычной остановки используйте `docker compose stop`. Команда `down --volumes` удаляет данные.

`scripts/init-local.ps1` создаёт `.local/dev.env` со случайными паролями и отдельным 32-байтовым `QAG_SECRET_KEY` в base64. Повторный запуск файл не перезаписывает. Ключ не хранится в PostgreSQL. Потеря или замена ключа делает прежние секреты недоступными; автоматической ротации ключа нет. Файл исключён из Git; доступ к нему ограничьте своей учётной записью.

## Резервная копия Compose

Остановите Go-приложение для согласованной точки ручной работы. PostgreSQL оставьте работающим:

```powershell
.\scripts\backup.ps1
# Либо отдельная новая папка:
.\scripts\backup.ps1 -Directory .local/backups/my-backup
```

Скрипт выполняет `pg_dump --format=custom --no-owner --no-acl`, копирует `qag.dump` из контейнера без текстового перенаправления PowerShell и создаёт `qag-secret-key.txt`. Файлы не перезаписываются. Ключ не печатается. Перенесите файл ключа в отдельное защищённое хранилище; дамп и ключ нужны вместе для восстановления секретов.

Скрипт предназначен для штатной БД `qag-postgres-1`. Для внешней PostgreSQL используйте её `pg_dump` той же основной версии и отдельно сохраните фактический мастер-ключ. Пароли БД приложения и целевого ресурса независимы.

## Восстановление в новую БД

Создайте отдельную пустую БД и проверьте восстановление. Пример для штатного Compose из корня проекта:

```powershell
$qagDocker = & .\scripts\docker-path.ps1
$qagBackup = (Resolve-Path .local/backups/my-backup/qag.dump).Path
& $qagDocker exec qag-postgres-1 createdb -U qag qag_restored
if ($LASTEXITCODE -ne 0) { throw 'Не удалось создать новую БД' }
& $qagDocker cp $qagBackup qag-postgres-1:/tmp/qag-restore.dump
if ($LASTEXITCODE -ne 0) { throw 'Не удалось скопировать дамп' }
& $qagDocker exec qag-postgres-1 pg_restore -U qag -d qag_restored --no-owner --no-acl --exit-on-error /tmp/qag-restore.dump
if ($LASTEXITCODE -ne 0) { throw 'Восстановление не завершено' }
& $qagDocker exec qag-postgres-1 rm -f -- /tmp/qag-restore.dump
```

Сделайте копию `.local/dev.env`. В рабочем файле замените имя базы в `QAG_DATABASE_URL` на `qag_restored` и установите `QAG_SECRET_KEY` из отдельной копии ключа. Запустите `scripts/run.ps1`. Проверьте каталог, историю и пробный вызов ресурса с секретом. Для возврата верните исходный локальный файл. Для внешней БД измените параметры подключения соответственно.

`TestBackupRestoreCatalogHistoryAndSecret` выполняет настоящий `pg_dump`/`pg_restore` между временными БД тестового контейнера. Проверяются сценарий, комментарии, причины сохранения, checksum миграций, расшифровка исходным ключом и отказ с другим ключом. Запуск — `scripts/test-integration.ps1`.

## Диагностика

| Симптом | Действие |
|---|---|
| Docker Engine недоступен | Запустите Docker Desktop, дождитесь Linux Engine |
| Virtual Machine Platform not enabled | Включите компонент Windows и перезагрузите компьютер согласно [Microsoft](https://learn.microsoft.com/en-us/windows/wsl/install-manual#step-3---enable-virtual-machine-feature) |
| PostgreSQL недоступна | Проверьте контейнер, порт, БД и пароль; пароль существующего тома сам не меняется |
| Не удалось подготовить схему | Нужны права создания схемы/таблиц/ICU collation; используйте штатный Compose |
| Схема несовместима | Запустите соответствующую версию бинарника; применённые миграции не редактируйте |
| Другой экземпляр использует БД | Оставьте один Go-процесс; второй не меняет данные первого |
| Адрес занят | Освободите порт или задайте другой loopback `QAG_ADDRESS` |
| Не удалось расшифровать секреты | Верните исходный `QAG_SECRET_KEY` |
| Результат не подтверждён PostgreSQL | Восстановите БД; повторяется только запись результата, не внешний вызов |
| `interrupted`, отмена или тайм-аут | Проверьте внешний эффект в целевой системе перед новой явной попыткой |

`GET /api/v1/health` показывает readiness только при доступной БД. Сервер принимает локальные Host и доверенный Origin; для Vite нужен `QAG_DEV_ORIGIN`. Логи не содержат строки подключения, мастер-ключа или тела протокольного запроса.
