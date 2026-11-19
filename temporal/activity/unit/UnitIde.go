package unit

import (
	"fmt"
	"runner/temporal/utils"
	"strings"
	"time"
)

const (
	// UnitIdePort — единственный порт IDE для всех юнитов. Он живёт
	// в network namespace контейнера unit и не публикуется на хост.
	UnitIdePort = 47000
	// ideHealthPath — путь health-эндпоинта code-server.
	ideHealthPath = "/healthz"
	// ideReadyTimeout — сколько ждём готовности IDE после docker compose up.
	ideReadyTimeout = 60 * time.Second
	// ideReadyPollInterval — пауза между проверками готовности IDE.
	ideReadyPollInterval = time.Second
	// ideReadyStepCommand — короткое имя шага готовности IDE в Steps.
	ideReadyStepCommand = "wait ide ready"
	// ideComposeService — имя сервиса IDE в compose-файле юнита.
	ideComposeService = "ide"
	// ideGitIndexSource — git-индекс рабочей копии на read-only томе IDE.
	ideGitIndexSource = "/home/coder/project/.git/index"
	// ideGitIndexTarget — путь, который IDE использует как GIT_INDEX_FILE.
	ideGitIndexTarget = "/tmp/git-index"
	// ideGitIndexStepCommand — короткое имя шага обновления git-индекса в Steps.
	ideGitIndexStepCommand = "ide-git-index"
)

// IdeGitIndexCommand собирает argv для копирования git-индекса внутри IDE.
// Копирование выполняется в контейнере ide, потому что /tmp/git-index живёт
// в его tmpfs и недоступен из контейнера unit.
func IdeGitIndexCommand() []string {
	return []string{
		"docker", "compose", "exec", "-T", ideComposeService,
		"sh", "-c", "cp " + ideGitIndexSource + " " + ideGitIndexTarget,
	}
}

// RefreshIdeGitIndex обновляет git-индекс внутри работающей IDE после
// синхронизации репозитория и томов. Если .git/index отсутствует, копирование
// завершается ошибкой: без индекса git diff в редакторе не работает.
func RefreshIdeGitIndex(unitPath string) error {
	return refreshIdeGitIndex(unitPath, utils.ExecCommand)
}

// refreshIdeGitIndex принимает исполнителя команд, чтобы тесты не требовали Docker.
func refreshIdeGitIndex(unitPath string, exec func(string, []string) (string, error)) error {
	if strings.TrimSpace(unitPath) == "" {
		return fmt.Errorf("не задан каталог юнита")
	}
	if exec == nil {
		return fmt.Errorf("не задан исполнитель команд")
	}

	if _, err := exec(unitPath, IdeGitIndexCommand()); err != nil {
		return fmt.Errorf("не удалось обновить git-индекс IDE, проверьте наличие %s: %w", ideGitIndexSource, err)
	}

	return nil
}

// IdeHealthURL возвращает адрес health-эндпоинта IDE в namespace юнита.
func IdeHealthURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", UnitIdePort, ideHealthPath)
}

// IdeHealthCheckCommand возвращает shell-команду проверки готовности IDE.
// Команда выполняется внутри контейнера unit, поэтому 127.0.0.1 указывает
// на общий network namespace, где слушает IDE.
func IdeHealthCheckCommand() string {
	return fmt.Sprintf("wget -q -O /dev/null %s", IdeHealthURL())
}

// WaitIdeReady ждёт, пока IDE начнёт отвечать по health-эндпоинту из
// network namespace юнита. Проверка выполняется внутри контейнера unit,
// потому что IDE использует network_mode: service:unit.
func WaitIdeReady(unitPath string) error {
	return waitIdeReady(unitPath, ideReadyTimeout, ideReadyPollInterval, ExecInUnitContainer)
}

// waitIdeReady принимает исполнителя команд, чтобы тесты не требовали Docker.
func waitIdeReady(
	unitPath string,
	timeout, interval time.Duration,
	exec func(string, string) (string, error),
) error {
	if strings.TrimSpace(unitPath) == "" {
		return fmt.Errorf("не задан каталог юнита")
	}
	if timeout <= 0 {
		return fmt.Errorf("таймаут ожидания IDE должен быть положительным")
	}
	if interval <= 0 {
		interval = time.Millisecond
	}
	if exec == nil {
		return fmt.Errorf("не задан исполнитель команд")
	}

	deadline := time.Now().Add(timeout)
	command := IdeHealthCheckCommand()
	lastErr := fmt.Errorf("IDE ещё не готова")

	for {
		_, err := exec(unitPath, command)
		if err == nil {
			return nil
		}

		lastErr = err

		if !time.Now().Before(deadline) {
			return fmt.Errorf("IDE не стала готова за %s: %w", timeout, lastErr)
		}

		sleep := interval
		if remaining := time.Until(deadline); sleep > remaining {
			sleep = remaining
		}
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
}
