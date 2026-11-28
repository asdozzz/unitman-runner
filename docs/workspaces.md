# Workspace-стенды в Go-runner

Workspace — это мультипроектный стенд для OpenCode: один контейнер с
вложенным Docker (dind) и отдельный multi-root IDE. В отличие от юнитов,
workspace работает не с одним проектом, а с набором проектов одной сессии и
даёт агенту общий рабочий каталог `/workspace`.

## Раскладка

```
<RUNNER_WORK_DIR>/workspaces/<WorkspaceId>/
  Dockerfile
  compose.yaml
  .env
  entrypoint.sh
  workspace-repo-sync.sh
  unit-skills-sync.sh
  seed/skills/               # staging OpenCode-навыков (host, read-only mount)
    manifest.json
    skills-index.md
    <containerName>/SKILL.md
  workspace.code-workspace   # генерируется внутри тома app
  ide/
    Dockerfile
    entrypoint.sh
    settings.json
```

Каталог создаётся активностью `ZapustitWorkspaceActivity`. Шаблоны лежат в
`temporal/activity/workspace/docker-dind/`; при запуске они копируются,
`compose.dind.yaml` становится `compose.yaml`, а `Dockerfile.temp` —
`Dockerfile`.

## Именованные тома

```
workspace_<WorkspaceId>_app       # /workspace
workspace_<WorkspaceId>_docker    # /var/lib/docker (dind)
workspace_<WorkspaceId>_opencode  # /root/.local/share/opencode
```

Тома external: они создаются `docker volume create` до `compose up` и
переживают `docker compose down -v`. Поэтому удаление workspace дополнительно
выполняет `docker volume rm -f` для этих трёх томов.

`container_name` основного контейнера — `${UNITMAN_WORKSPACE_SLUG}.workspaces`,
IDE — `${UNITMAN_WORKSPACE_SLUG}.ide`.

## Переменные `.env`

```
UNITMAN_WORKSPACE_ID
UNITMAN_WORKSPACE_NAME
UNITMAN_WORKSPACE_SLUG
UNITMAN_BRANCH_NAME
UNITMAN_READ_ONLY
UNITMAN_MEMORY_LIMIT
COMPOSE_PROJECT_NAME
UNITMAN_WORKSPACE_APP_VOLUME
UNITMAN_WORKSPACE_DOCKER_VOLUME
UNITMAN_WORKSPACE_OPENCODE_VOLUME
```

`UNITMAN_MEMORY_LIMIT` приходит числом мегабайт (по умолчанию 3072) и
записывается как `<N>m`.

## Проекты

Каждый проект клонируется в `/workspace/<ProjectId>` (ключ — `ProjectId`, он
же используется в `ProveritIzmeneniyaWorkspaceActivity`). Ветка задаётся
общей для workspace переменной `UNITMAN_BRANCH_NAME`.

`workspace-repo-sync.sh`:

- при первом запуске делает `git clone --branch <branch>`; если ветки нет в
  origin, клонирует базовую ветку `UNITMAN_REPO_BASE_BRANCH` (или дефолтную
  ветку origin, если базовая не задана или недоступна) и создаёт ветку
  workspace локально через `git checkout -b` — без upstream;
- при повторном — обновляет `origin`, делает fetch, сохраняет локальные
  изменения через stash/pop; если `origin/<branch>` существует, переключает
  ветку и выполняет `git pull --rebase --autostash`; если ветки нет в origin,
  но локальная ветка существует, просто переключается на неё без `pull`
  (upstream отсутствует); если ветки нет ни в origin, ни локально — ошибка
  `opencode.workspace.project_branch_not_found`;
- при `UNITMAN_READ_ONLY=true` удаляет учётные данные из `origin` и ставит
  `pre-push`-хук, запрещающий push;
- при ошибке печатает `UNITMAN_ERROR_KEY=<key>`; активность переносит ключ в
  `Projects[].ErrorKey`.

`workspace.code-workspace` содержит папки всех проектов и генерируется после
успешной синхронизации.

## OpenCode-навыки (skills)

Навыки резолвятся для всех `Projects[].ProjectId` после
`prepareWorkspaceFiles` и до `compose up --build`, затем раскладываются в
`<workspace>/seed/skills` (тот же макет, что у юнита: `manifest.json`,
`skills-index.md`, `<containerName>/SKILL.md`). Каталог монтируется в
контейнер read-only:

```yaml
- ./seed/skills:/opt/unitman/skills:ro
```

`unit-skills-sync.sh` попадает в образ workspace через `Dockerfile.temp` и
вызывается из `entrypoint.sh`, а также перед каждым ходом агента
(`unit-skills-sync --if-changed`). Материализация:

```
$HOME/.config/opencode/skill/<containerName>/SKILL.md
$HOME/.config/opencode/skills-index.md
$HOME/.config/opencode/.unitman-skills-hash
/app/.unitman/skills-manifest.json      # только диагностика
```

`OPENCODE_CONFIG_CONTENT` workspace-хода также содержит
`"instructions": ["/root/.config/opencode/skills-index.md"]`.

При `ObnovitWorkspaceActivity` (добавление/удаление проектов) набор навыков
перечитывается и раскладывается заново, после чего выполняется
`unit-skills-sync --if-changed` в работающем контейнере. Если контейнер
остановлен или bind-mount ещё не подхватил новую раскладку, обновление
материализуется на следующем ходе агента. Ошибки навыков всегда некритичны.

## Активности

Все активности регистрируются на собственной очереди раннера — `taskQueue` из
его `config.json` (равна `runnerId`). Общей/legacy-очереди нет.

| Активность | Вход | Поведение |
|---|---|---|
| `ZapustitWorkspaceActivity` | `WorkspaceId`, `WorkspaceName`, `WorkspaceSlug`, `Branch`, `ReadOnly`, `MemoryLimit`, `Projects` | раскладка, тома, `compose up -d --build`, ожидание health workspace и IDE, клонирование проектов, `workspace.code-workspace` |
| `OstanovitWorkspaceActivity` | `WorkspaceId` | только `docker compose stop` |
| `UdalitWorkspaceActivity` | `WorkspaceId` | `docker compose down -v --remove-orphans`, удаление томов и каталога |
| `ProveritIzmeneniyaWorkspaceActivity` | `WorkspaceId`, `Projects[{ProjectId, ProjectName}]` | `git status --porcelain` в каждом проекте |

`ZapustitWorkspaceActivity` идемпотентна: повторный запуск обновляет файлы,
переиспользует существующие каталоги и тома и не пересоздаёт проекты.
Шаги публикуются через `api/runner/saveSteps` с `ResponseId = workflowId`.

## Жизненный цикл

### Создание (create)

1. `mkdir -p <workdir>/workspaces/<WorkspaceId>`;
2. копирование шаблонов, генерация `Dockerfile`, `compose.yaml`, `.env`;
3. `docker volume create` трёх томов;
4. `docker compose up -d --build`;
5. ожидание `workspace` (`/tmp/.workspace_ready`, docker.sock, `docker info`)
   и healthcheck IDE;
6. синхронизация проектов;
7. генерация `workspace.code-workspace`.

### Archive / stop

Остановка — это `docker compose stop`. Контейнеры остаются, тома и каталог
сохраняются. Возобновление (`restore`/`start`) — повторный
`ZapustitWorkspaceActivity`: он обновляет раскладку и поднимает контейнеры,
данные томов сохраняются.

Архивация на уровне домена (статус `ARCHIVED`) не удаляет тома: содержимое
`/workspace`, docker-образы проекта и данные OpenCode остаются в
`workspace_<WorkspaceId>_*`.

### Delete

Удаление — `docker compose down -v --remove-orphans`, затем явное
`docker volume rm -f` для трёх external-томов и удаление каталога
`<workdir>/workspaces/<WorkspaceId>`. Операция идемпотентна.

## Read-only

Read-only включается, если `ReadOnly=true` у workspace или `CanPush=false`
хотя бы у одного проекта:

- в промпт OpenCode CLI добавляется запрет на `git commit` и `git push`;
- `workspace-repo-sync.sh` убирает токен из `origin` и ставит
  `pre-push`-хук.

## OpenCode CLI

Для workspace `HodCommand` получает `WorkspaceId`, `WorkspaceProjects`,
`WorkspaceReadOnly`. Рабочий каталог хоста —
`<base>/workspaces/<WorkspaceId>` (контекст `docker compose exec`), корни —
контейнерные подкаталоги `/workspace/<ProjectId>`: проекты живут в томе
`workspace_<id>_app` и на хосте не видны. В контейнере CLI запускается в
`/workspace`:

```
opencode run --format json --auto --model unitman/main --dir /workspace ...
```

## Файлы workspace

Вложения лежат в зарезервированном каталоге `/workspace/unitman_files`, рядом
с каталогами проектов (проекты называются по `ProjectId`, поэтому коллизия
невозможна). Каталог добавляется в `workspace.code-workspace` отдельной
папкой, если в workspace есть файлы.

- `ZapustitWorkspaceActivity` принимает `Files[]` (`FileId`, `Name`, `Url`),
  скачивает каждый файл на хост, копирует в контейнер через
  `docker compose cp` и выставляет `chmod 0444`;
- имя проверяется `validWorkspaceFileName`: запрещены `/`, `\`, переводы
  строк, NUL, `.`/`..`, имена с ведущей точкой и резервные
  `workspace.code-workspace` / `unitman_files`;
- `ObnovitWorkspaceActivity` клонирует только новые проекты, перегенерирует
  `workspace.code-workspace`, применяет `Files[]` и удаляет
  `FilesToDelete[]` (имена файлов), не пересоздавая контейнер.

`/workspace/unitman_files` — **только входные данные**, смонтированные
read-only. Как outbox он не используется: агент не должен менять входные файлы,
а раннер не сканирует этот каталог.

## Артефакты хода (outbox)

Готовые документы агента (`план`, отчёт, спецификация) забираются из отдельного
каталога-исходящего ящика. Механика — [`artifacts.go`](../temporal/activity/opencode/artifacts.go:1).

### Жизненный цикл

1. Перед `opencode run` для workspace-хода (`HodCommand.WorkspaceID` задан)
   раннер выполняет `rm -rf`, `mkdir -p` и `chmod 0775` для
   `/workspace/unitman_artifacts/<workflowId>/`;
2. в промпт добавляется инструкция сохранять документы в этот каталог
   ([`cliArtifactInstruction`](../temporal/activity/opencode/cli.go:237));
   HTTP-исполнитель поле `ArtifactDir` не читает;
3. после успешного ответа раннер собирает файлы каталога, загружает их на
   бэкенд и удаляет каталог через `rm -rf` (best effort, ошибка только
   логируется);
4. ошибка подготовки каталога не валит ход: артефакты просто отключаются.

Юнит-ходы и свободные сессии каталог не создают и `ArtifactDir` не получают.

### Сбор файлов

```sh
find /workspace/unitman_artifacts/<workflowId> -maxdepth 1 -type f -printf '%f\t%s\n'
```

- учитываются только регулярные файлы на глубине 1; подкаталоги игнорируются;
- отсутствие каталога — не ошибка;
- файлы сортируются по имени, чтобы результат был детерминирован;
- имя проверяется [`validArtifactName`](../temporal/activity/opencode/artifacts.go:80):
  запрещены `/`, `\`, NUL, `\r`, `\n`, `.`, `..`, имена с ведущей точкой и
  длиннее 128 символов; расширение берётся в нижнем регистре.

### Лимиты

Лимиты зеркалят бэкенд:

| Лимит | Значение |
|---|---|
| файлов на ход | 5 |
| размер файла | 2 МиБ (`maxArtifactBytes`) |
| суммарный размер | 8 МиБ (`maxTotalArtifactBytes`) |
| длина имени | 128 символов |

Файлы сверх лимитов и файлы нулевого размера пропускаются.

### Типы и содержимое

Allowlist (расширение → MIME):

```text
md   text/markdown
txt  text/plain
json application/json
yaml application/yaml
yml  application/yaml
csv  text/csv
log  text/plain
```

Жёсткий denylist независимо от allowlist: `html`, `htm`, `xhtml`, `svg`, `js`,
`mjs`, `ts`, `php`, `sh`, `py`, `exe`, `bin`, `so`, `dll`, `bat`, `cmd`, `ps1`,
`sql`.

Файл копируется на хост через `docker compose cp` и читается целиком. Затем
повторно проверяются размер (`0 < size <= 2 МиБ`), отсутствие NUL-байтов и
валидность UTF-8; при нарушении файл пропускается.

### Загрузка

```http
POST <backend>/api/runner/opencode/artifact?workflowId=<id>&workspaceId=<id>&name=<name>&contentType=<mime>
X-Runner-Id: <runner id>
X-Runner-Signature: hex(HMAC-SHA256(secret, workflowId + "\n" + name + "\n" + body))
Content-Type: application/octet-stream

<сырые байты>
```

Имя и `contentType` кодируются в query, но подпись считается по исходному
(декодированному) имени и сырому телу. Ответ бэкенда — `{status, data}`, где
`data` содержит `FileId`, `Name`, `Size`, `ContentType`; эти метаданные
попадают в `HodResult.Attachments`.

Пустой `workflowId` или пустой `BackendURL` означают «загрузка отключена»:
файл пропускается без ошибки.

### Повторы и нефатальность

- до 3 попыток на файл;
- пауза между попытками — 300 мс;
- таймаут одной попытки — 30 секунд;
- повтор только при сетевой ошибке или ответе 5xx; 4xx не повторяются.

Сбой сбора, копирования или загрузки не меняет `Success`/`Kind` хода и не
возвращает ошибку активности: он пишется в лог и в шаг прогресса
(`artifact <имя>`). Паника при сборе артефактов перехватывается. Исходящий ящик
удаляется после попытки загрузки в любом случае.

## Очистка хостового Docker

`monitor.RunCleanup` выполняет только `docker image prune -af` и
`docker builder prune -af`. Он не вызывает `container prune`, `volume prune`,
`system prune` и не использует `--volumes`, поэтому остановленные контейнеры
и тома workspace не затрагиваются.
