# Состояние раннера unitman-runner

## Назначение

Раннер хранит ожидаемое и фактическое состояние контейнеров юнитов в локальной
базе bbolt. Это позволяет фоновым циклам:

- отправлять health-push на backend (`/api/runner/health`);
- сверять ожидаемое состояние контейнеров с фактическим (`docker ps`);
- уведомлять backend о расхождениях (`/api/runner/unitStateNotifications`);
- не спамить одинаковыми уведомлениями: факт отправки фиксируется в БД;
- восстанавливать состояние после перезапуска раннера.

Ранее эти задачи выполнялись Temporal-активностями
`RunnerHealthCheckActivity` и `OchistkaDockeraActivity`. Теперь они удалены, а
мониторинг работает как фоновые горутины внутри процесса раннера (`monitor`).

## Раскладка и предстартовые проверки

- Бинарь раннера самодостаточен: шаблоны unit/workspace и конфиги proxy
  встроены через `go:embed` и распаковываются при `start` в рабочий каталог.
- `UNITMAN_HOME` задаёт рабочий каталог (`config.json`, `projects/`,
  `state.db`); по умолчанию используется текущий каталог.
- Перед запуском воркера выполняется preflight: `docker info` должен
  содержать runtime `sysbox-runc`. Fallback на privileged dind не
  предусмотрен.
- Для локальной отладки без sysbox: `UNITMAN_SKIP_PREFLIGHT=1`.

## Конфигурация раннера (multi-runner)

Идентичность раннера хранится в `config.json` и создаётся командой
`runner init` после успешной регистрации на backend:

| Ключ `config.json` | Назначение |
|---|---|
| `TemporalHost` (`TemporalHost`) | `host:ports.temporal` из ответа регистрации. |
| `ApiHost` (`ApiHost`) | `scheme://host:ports.api` backend API. |
| `RunnerID` (`runnerId`) | UUID v4, выданный backend-ом. Обязателен для запуска. |
| `RunnerSecret` (`runnerSecret`) | Per-runner секрет HMAC-SHA256 (64 hex-символа) для всех callback-подписей. |
| `RegistryHost` (`registryHost`) | `scheme://<registryHost>:ports.registry` из ответа регистрации. |

`runner init` спрашивает **только host backend-а** (пустой ответ — `localhost`;
схема и завершающий слэш ввода отбрасываются). Затем выполняется публичный
`POST http://<host>:8082/api/runner/register` с телом
`{"hostname":"...","version":"..."}`. Регистрация **не подписывается**: никаких
секретов и auth-заголовков не отправляется.

Успешный ответ `201`:

```json
{
  "runnerId": "uuid",
  "taskQueue": "uuid",
  "secret": "<64 hex>",
  "registryHost": "registry.example.com",
  "ports": {"api": 8082, "temporal": 7233, "registry": 5000}
}
```

`runner init` проверяет, что `taskQueue` совпадает с `runnerId`, а `secret` —
64 hex-символа. Порты из ответа задают `ApiHost`, `TemporalHost` и
`RegistryHost`; при отсутствии отдельного порта используются значения по
умолчанию `8082`/`7233`/`5000`. Если `registryHost` пуст, реестр собирается из
host-а регистрации и registry-порта.

`config.json` записывается атомарно (временный файл в той же директории +
`rename`) с правами `0600`, потому что содержит `RunnerSecret`. Секрет никогда
не читается из переменных окружения.

Воркер слушает Temporal-очередь, равную `RunnerID`; отдельная `taskQueue` не
хранится. Legacy-константа `unitman-runner-queue` в раннере не используется.

### Callback-подписи

Каждый callback раннера содержит `X-Runner-Id` и
`X-Runner-Signature = hex(HMAC-SHA256(payload, RunnerSecret))` (нижний регистр).
`X-Runner-Id` всегда равен `runnerId` из `config.json` и совпадает с телом
(health/unitState) или путём (build-state).

| Endpoint | Подписываемый payload |
|---|---|
| `POST /api/runner/health` | сырое тело запроса |
| `POST /api/runner/unitStateNotifications` | сырое тело запроса |
| `POST /api/runner/saveSteps` | конкатенация `responseId + stepsContent` (не JSON-тело) |
| `GET .../build-state` | пустой payload |
| `PUT .../build-state` | сырое тело запроса |

Backend отвечает `404 runner.not_found` для неизвестного `runnerId` и
`401 runner.invalid_signature` при неверной подписи. Публичный адрес раннера (`publicAddress`)
задаётся администратором на backend-е и в `config.json` раннера
не хранится.

## Файл БД и persist-требования

По умолчанию состояние хранится в файле `runner-state.db` в рабочем каталоге
раннера (путь переопределяется переменной `RUNNER_STATE_DB`).

Требования:

- Файл БД **обязательно** должен лежать на постоянном диске / Docker volume.
  При потере файла раннер восстановит часть состояния из каталогов
  `projects/*/units/*` при старте (bootstrap), но история уведомлений и точные
  метки расхождений будут утеряны.
- Каталог для файла должен быть доступен на запись пользователю, под которым
  запущен раннер.
- Если открыть БД не удалось, раннер не падает: используется no-op store, а
  ошибка пишется в лог. Мониторинг при этом продолжает работать, но состояние
  не сохраняется.

## Переменные окружения

| Переменная | Назначение | Значение по умолчанию |
|---|---|---|
| `RUNNER_STATE_DB` | Путь к файлу bbolt с состоянием юнитов | `runner-state.db` |
| `RUNNER_WORK_DIR` | Рабочий каталог раннера, корень раскладки `projects/<ProjectId>/units/<UnitId>` | `os.Getwd()` |
| `RUNNER_BACKEND_URL` | Базовый URL backend unitman | `ApiHost` из `config.json` |
| `RUNNER_RESOURCES_INTERVAL_SECONDS` | Период сбора снимка ресурсов (CPU, память, диски, Docker) | `30` |
| `RUNNER_THREAT_WARNING_PERCENT` | Жёлтый порог потребления памяти/диска | `70` |
| `RUNNER_THREAT_CRITICAL_PERCENT` | Красный порог потребления памяти/диска | `85` |
| `RUNNER_THREAT_BLOCKER_PERCENT` | Чёрный порог: блокировка ресурсоёмких активностей | `90` |

Пустые значения трактуются как «переменная не задана» — применяется значение по
умолчанию.

## Расписание циклов

| Цикл | Интервал | Что делает |
|---|---|---|
| Resources | 30 секунд | Собирает снимок ресурсов хоста: CPU (`/proc/stat`, `/proc/loadavg`), память (`/proc/meminfo`), диски (`statfs` для `/`, рабочего каталога и `DockerRootDir`), занятое Docker место (`/system/df`), и публикует его в пакет `threat`. |
| Health-push | 1 минута | Отправляет `docker stats` (для юнитов `MemPerc`/`MemUsage` пересчитаны от лимита памяти), `/proc/meminfo`, `resources` (снимок с уровнями), `runnerId`, `taskQueue`, `registryHost`, `unixtime` на `/api/runner/health`. |
| Reconciler | 10 минут | Сверяет ожидаемое состояние из БД с фактическим (`docker ps`), отправляет уведомления о расхождениях и восстановлениях. |
| Bootstrap | один раз при старте (внутри reconciler) | Обходит `projects/*/units/*`, читает `.env` и создаёт записи состояния для существующих юнитов. |
| Cleanup | 2 часа | Выполняет `docker image prune -af` и `docker builder prune -af`. |

Все циклы запускаются в отдельных горутинах и завершаются по `SIGINT` /
`SIGTERM` через `signal.NotifyContext`.

## Устойчивость

- **Bootstrap при старте.** Состояние существующих юнитов восстанавливается из
  каталогов `projects/<ProjectId>/units/<UnitId>` (файлы `compose.yaml` и
  `.env`). Ожидаемое состояние при bootstrap выставляется равным фактическому,
  поэтому ложных уведомлений сразу после старта не возникает.
- **Без спама уведомлений.** Для каждого юнита хранится `lastNotifiedActual` и
  `lastNotifiedAt`. Повторное уведомление об одном и том же расхождении
  отправляется не чаще, чем раз в 30 минут.
- **Блокировка при исчерпании ресурсов.** Снимок ресурсов несёт уровни
  `ok` / `warning` (≥70%) / `critical` (≥85%) / `blocker` (≥90%) по памяти и
  каждому диску; общий уровень — худший из них. На уровне `blocker`
  раннер отказывает ресурсоёмким активностям (сборка, подготовка, запуск и
  обновление юнита, IDE-сессия, инициализация проекта, запуск workspace) с
  шагом «проверить ресурсы сервера». Остановка, удаление, prune и read-only
  проверки остаются разрешёнными, чтобы ресурсы можно было освободить.
  Backend дополнительно предпроверяет уровень в `RunnerApi` и возвращает
  локализованную ошибку `runner.resources.blocker`.
- **Статистика памяти относительно лимита.** Базой health-push остаётся вывод
  `docker stats --no-stream --format json` (CPU, сеть, перечень контейнеров).
  Для контейнеров юнитов (`<UNITMAN_UNIT_NAME>.<UNITMAN_PROJECT_NAME>`) раннер
  дополнительно обращается к Docker Engine API: `GET /containers/<name>/json`
  (`HostConfig.Memory` — фактический лимит) и
  `GET /containers/<name>/stats?stream=false` (`memory_stats.usage`). Затем
  `MemPerc` пересчитывается как `usage / лимит`, а `MemUsage` — как
  `<usage> / <лимит>`, чтобы процент не считался от общей памяти хоста.
  Приоритет лимита: `HostConfig.Memory`, затем `UNITMAN_MEMORY_LIMIT` из
  `.env` юнита, затем дефолт compose `3g`. Адрес daemon берётся из `DOCKER_HOST`
  (`unix://`, `tcp://`, `http://`; по умолчанию `/var/run/docker.sock`).
  Если API недоступен, строки уходят без изменений — health-push не ломается.
- **Постоянный диск.** Для сохранения истории между перезапусками файл
  `RUNNER_STATE_DB` должен размещаться на постоянном диске или в Docker volume.
- **Ошибка БД не фатальна.** При проблемах с открытием bbolt раннер продолжает
  обслуживать Temporal-очередь и мониторинг с no-op store.
