package unit

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	unitRepoScriptPath = "/usr/local/bin/unit-repo-sync"
	unitConfigPath     = "/app/unitman.yaml"
)

// SyncUnitRepo выполняет seed или обновление git-репозитория внутри юнита.
// Push не выполняется: обновление только забирает изменения и сохраняет
// локальные правки через rebase --autostash.
func SyncUnitRepo(unitPath, branch, storageURL string) (string, error) {
	return ExecInUnitContainerWithOptions(unitPath, unitRepoScriptPath, UnitExecOptions{
		Env: []string{
			"UNITMAN_REPO_BRANCH=" + branch,
			"UNITMAN_REPO_URL=" + storageURL,
		},
	})
}

// ReadUnitConfig читает unitman.yaml из рабочего дерева внутри юнита.
// Отсутствие файла — не ошибка: конфиг опционален.
func ReadUnitConfig(unitPath string) (string, error) {
	return ExecInUnitContainer(unitPath, "if [ -f "+unitConfigPath+" ]; then cat "+unitConfigPath+"; fi")
}

// ReadEnvFile разбирает .env, который runner пишет в каталог юнита.
func ReadEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	values := map[string]string{}

	for _, line := range strings.Split(string(data), "\n") {
		preparedLine := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if preparedLine == "" || strings.HasPrefix(preparedLine, "#") {
			continue
		}

		key, value, found := strings.Cut(preparedLine, "=")
		if !found {
			continue
		}

		values[strings.TrimSpace(key)] = value
	}

	return values, nil
}

// ReadUnitEnvValue возвращает значение переменной из .env юнита.
func ReadUnitEnvValue(unitPath, key string) string {
	values, err := ReadEnvFile(filepath.Join(unitPath, ".env"))
	if err != nil {
		return ""
	}

	return values[key]
}

// RedactStorageURL скрывает учётные данные в URL репозитория, чтобы токен
// не попадал в шаги прогресса.
func RedactStorageURL(rawURL string) string {
	preparedURL := strings.TrimSpace(rawURL)
	if preparedURL == "" {
		return ""
	}

	parsedURL, err := url.Parse(preparedURL)
	if err == nil && parsedURL.User != nil {
		parsedURL.User = url.User("redacted")
		return parsedURL.String()
	}

	if strings.Contains(preparedURL, "@") {
		return "<redacted>"
	}

	return preparedURL
}
