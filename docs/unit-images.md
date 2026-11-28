# Предсобранные образы юнита и IDE по требованию

## Образы

Подготовка юнита не собирает образы на месте. Используются два предсобранных
образа:

| Образ | Источник | Содержимое |
|---|---|---|
| `unitman/unit` | [`Dockerfile.Dind`](../Dockerfile.Dind) | `docker:dind`, OpenCode, git, entrypoint и скрипты синхронизации |
| `unitman/ide` | [`ide/Dockerfile`](../temporal/activity/unit/docker-dind/ide/Dockerfile) | code-server, git/wget, настройки и entrypoint IDE |

Оба образа помечены `LABEL unitman.keep=true`, поэтому очистка раннера
(`docker image prune -af --filter label!=unitman.keep=true`) их не удаляет.

## Откуда берутся ссылки на образы

Ссылки фиксируются при сборке раннера и не читаются из `unitman.yaml`:

```dotenv
UNITMAN_UNIT_IMAGE=<registryHost>/unitman/unit:<releaseVersion>
UNITMAN_IDE_IMAGE=<registryHost>/unitman/ide:<releaseVersion>
```

- `<registryHost>` приходит из ответа регистрации раннера (`config.json`).
- Репозитории и тег задаются при сборке через ldflags
  (`runner/config.UnitImageRepository`, `IdeImageRepository`, `UnitImageTag`)
  и по умолчанию используют `BuildVersion`.
- Окружение процесса раннера и конфиг проекта образы не переопределяют.

## Сборка для разработки

```sh
make images-dev
```

Собирает локальные теги:

- `unitman/unit:dev`
- `unitman/ide:dev`

Для release-сборки и публикации:

```sh
make images-push
```

Отдельные цели: `dind-dev`, `ide-dev`, `dind-build`, `ide-build`,
`dind-push`, `ide-push`.

## Запуск с dev-образами

Для локальной проверки соберите образ и опубликуйте его в реестре установки
под именем `unitman/unit` или `unitman/ide` с нужным тегом, либо соберите
раннер с переопределёнными ldflags:

```sh
go build -ldflags "\
  -X runner/config.UnitImageTag=dev" -o main .
```

## OpenCode-навыки (skills)

Перед `docker compose up` раннер запрашивает эффективные навыки проекта у
backend-а (`POST /api/internal/opencode/skills/resolve`, подпись
`X-Runner-Signature`) и раскладывает их в staging-каталог юнита:

```
<unit>/seed/skills/
  manifest.json                 # manifestHash, список навыков, error-маркер
  skills-index.md               # детерминированный индекс для агента
  <containerName>/SKILL.md      # YAML frontmatter + тело навыка
```

Каталог создаётся `EnsureUnitSeedDir` ещё до `compose up` (иначе Docker создаст
его root-owned) и монтируется read-only:

```yaml
- ./seed/skills:/opt/unitman/skills:ro
```

Внутри контейнера `unit-skills-sync` (entrypoint и каждый ход агента) копирует
навыки в конфиг OpenCode:

```
$HOME/.config/opencode/skill/<containerName>/SKILL.md
$HOME/.config/opencode/skills-index.md
$HOME/.config/opencode/.unitman-skills-hash
/app/.unitman/skills-manifest.json      # только диагностика
```

`OPENCODE_CONFIG_CONTENT` дополнительно содержит
`"instructions": ["/root/.config/opencode/skills-index.md"]` — fallback, если
OpenCode не обнаруживает `skill/` автоматически.

`unit-skills-sync --if-changed` сравнивает `manifestHash` с сохранённым hash и
при совпадении ничего не делает. Скрипт best-effort: при отсутствии источника
или ошибке он логирует проблему в stderr и всегда завершается с кодом 0.

Деградация: 404 от resolve означает «фича не развёрнута»; ошибка сети или
записи не валит подготовку юнита — юнит просто останется без навыков, а в шаг
прогресса попадёт сообщение `non-fatal: ...`.

## Подготовка и запуск IDE

1. Подготовка юнита выполняет `docker compose up -d` и поднимает только
   контейнер `unit`.
2. Сервис `ide` объявлен с `profiles: ["ide"]` и при обычном `up` не стартует.
3. Режим IDE (`view` или `edit`) хранится в файле `ide-mode` и применяется
   выбором статичного override-файла:
   - `compose.ide-view.yaml` — рабочая копия `:ro`, git-индекс в `/tmp`;
   - `compose.ide-edit.yaml` — рабочая копия `:rw`, запуск от root,
     `UNITMAN_IDE_EDIT_MODE=1`.

   Раннер выполняет
   `docker compose -f compose.yaml -f compose.ide-<mode>.yaml --profile ide up -d --no-deps ide`
   и ждёт health-эндпоинт. Файлы compose не переписываются в рантайме.

## Ожидание первого запуска

Прокси раннера отдаёт для порта IDE страницу «Редактор запускается…» с
автообновлением на 502/503/504. Поэтому первое открытие ссылки не показывает
голый 502, пока контейнер `ide` ещё поднимается.

Порт в [`nginx.conf`](../.docker/proxy/nginx.conf) должен совпадать с
`UNITMAN_IDE_PORT` (по умолчанию `47000`).

## Workspace

Workspace пока использует прежнюю схему: IDE поднимается вместе с workspace.
Перевод workspace на предсобранные образы и профиль — отдельный этап.
