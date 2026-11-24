# Предсобранные образы юнита и IDE по требованию

## Образы

Подготовка юнита больше не собирает образы на месте. Используются два
предсобранных образа:

| Образ | Источник | Содержимое |
|---|---|---|
| `asdozzz/dind-unit:latest` | [`Dockerfile.Dind`](../Dockerfile.Dind) | `docker:dind`, OpenCode, git, entrypoint и скрипты синхронизации |
| `asdozzz/unitman-ide:latest` | [`ide/Dockerfile`](../temporal/activity/unit/docker-dind/ide/Dockerfile) | code-server, git/wget, настройки и entrypoint IDE |

Оба образа помечены `LABEL unitman.keep=true`, поэтому очистка раннера
(`docker image prune -af --filter label!=unitman.keep=true`) их не удаляет.

## Сборка для разработки

```sh
make images-dev
```

Собирает локальные теги:

- `unitman/dind:dev`
- `unitman/ide:dev`

Для release-сборки и публикации:

```sh
make images-push
```

Отдельные цели: `dind-dev`, `ide-dev`, `dind-build`, `ide-build`,
`dind-push`, `ide-push`.

## Как использовать dev-образы

- Базовый образ юнита задаётся в `unitman.yaml` проекта:

  ```yaml
  build:
    image: unitman/dind:dev
  ```

  или release-тегом `asdozzz/dind-unit:latest`.

- Образ IDE не настраивается в `unitman.yaml`. Для разработки задайте
  переменную окружения процесса раннера:

  ```sh
  UNITMAN_IDE_IMAGE=unitman/ide:dev ./main start
  ```

  Раннер прокидывает её в `.env` юнита, compose использует
  `${UNITMAN_IDE_IMAGE:-asdozzz/unitman-ide:latest}`.

## Подготовка и запуск IDE

1. Подготовка юнита выполняет `docker compose up -d` и поднимает только
   контейнер `unit`.
2. Сервис `ide` объявлен с `profiles: ["ide"]` и при обычном `up` не стартует.
3. IDE запускается по требованию:
   - при запросе персональной ссылки редактора (`view` и `edit`);
   - при включении режима редактирования.

   Раннер выполняет `docker compose --profile ide up -d --no-deps ide` и ждёт
   health-эндпоинт.

## Ожидание первого запуска

Прокси раннера отдаёт для порта IDE страницу «Редактор запускается…» с
автообновлением на 502/503/504. Поэтому первое открытие ссылки не показывает
голый 502, пока контейнер `ide` ещё поднимается.

Порт в [`nginx.conf`](../.docker/proxy/nginx.conf) должен совпадать с
`UNITMAN_IDE_PORT` (по умолчанию `47000`).

## Workspace

Workspace пока использует прежнюю схему: IDE поднимается вместе с workspace.
Перевод workspace на предсобранные образы и профиль — отдельный этап.
