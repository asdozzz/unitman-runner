package unit

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"runner/temporal/credentials"
)

const (
	unitRepoScriptPath = "/usr/local/bin/unit-repo-sync"
	unitConfigPath     = "/app/unitman.yaml"
)

// SyncUnitRepo выполняет seed или обновление git-репозитория внутри юнита.
// Push не выполняется: обновление только забирает изменения и сохраняет
// локальные правки через rebase --autostash.
//
// URL обязан быть чистым; креденшелы передаются через askpass-файл,
// скопированный в контейнер, и удаляются сразу после операции.
func SyncUnitRepo(
	ctx context.Context,
	unitPath, branch, storageURL string,
	grant credentials.Grant,
) (string, error) {
	cleanURL, err := CleanRepoURL(storageURL)
	if err != nil {
		return "", err
	}

	credEnv, cleanup, err := InstallGitCredentialsForUnit(ctx, unitPath, unitComposeService, grant)
	if err != nil {
		return "", err
	}
	defer cleanup()

	env := []string{
		"UNITMAN_REPO_BRANCH=" + branch,
		"UNITMAN_REPO_URL=" + cleanURL,
	}
	env = append(env, credEnv...)

	return ExecInUnitContainerContext(ctx, unitPath, unitRepoScriptPath, UnitExecOptions{Env: env})
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
