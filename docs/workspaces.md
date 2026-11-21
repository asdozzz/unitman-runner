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

## Активности

Все активности регистрируются на очереди `unitman-runner-queue`.

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

## Очистка хостового Docker

`monitor.RunCleanup` выполняет только `docker image prune -af` и
`docker builder prune -af`. Он не вызывает `container prune`, `volume prune`,
`system prune` и не использует `--volumes`, поэтому остановленные контейнеры
и тома workspace не затрагиваются.
