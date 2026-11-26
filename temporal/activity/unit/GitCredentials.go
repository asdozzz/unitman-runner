package unit

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"runner/temporal/credentials"
	"runner/temporal/utils"
)

// fetchRepoGrant — точка подмены для тестов активностей.
var fetchRepoGrant = credentials.FetchFromConfig

const (
	// Пути внутри контейнера. Секрет живёт только в файле с 0600 и удаляется
	// сразу после git-операции.
	containerAskpassPath = "/tmp/unitman-git-askpass.sh"
	containerGitCredPath = "/tmp/unitman-git-credentials"
)

// CleanRepoURL проверяет, что remote URL не содержит userinfo. Токен в URL
// запрещён: он должен передаваться только через askpass.
func CleanRepoURL(raw string) (string, error) {
	prepared := strings.TrimSpace(raw)
	if prepared == "" {
		return "", errors.New("не задан URL репозитория")
	}

	parsed, err := url.Parse(prepared)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("некорректный URL репозитория")
	}
	if parsed.User != nil {
		return "", errors.New("URL репозитория содержит учётные данные; используйте broker-грант")
	}

	return prepared, nil
}

// writeAskpassTemp создаёт на хосте временный askpass-скрипт и файл с
// username/token. scriptCredPath — путь к файлу креденшелов в контексте,
// где будет выполняться git (хост или контейнер). Пустое значение — локальный
// путь staging-каталога.
func writeAskpassTemp(baseDir string, grant credentials.Grant, scriptCredPath string) (string, error) {
	dir, err := os.MkdirTemp(baseDir, ".unitman-git-credentials-")
	if err != nil {
		return "", fmt.Errorf("создать staging git-креденшелов: %w", err)
	}

	if err := os.Chmod(dir, 0700); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("ограничить staging git-креденшелов: %w", err)
	}

	credPath := filepath.Join(dir, "credentials")
	if err := os.WriteFile(credPath, []byte(grant.Username+"\n"+grant.Token+"\n"), 0600); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("записать git-креденшелы: %w", err)
	}

	if scriptCredPath == "" {
		scriptCredPath = credPath
	}

	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"    *Username*) sed -n '1p' " + shellQuote(scriptCredPath) + " ;;\n" +
		"    *Password*) sed -n '2p' " + shellQuote(scriptCredPath) + " ;;\n" +
		"    *) exit 0 ;;\n" +
		"esac\n"

	askpassPath := filepath.Join(dir, "git-askpass.sh")
	if err := os.WriteFile(askpassPath, []byte(script), 0700); err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("записать askpass: %w", err)
	}

	return dir, nil
}

// hostAskpassEnv возвращает env для git на хосте. В argv секретов нет.
func hostAskpassEnv(stagingDir string) []string {
	return []string{
		"GIT_ASKPASS=" + filepath.Join(stagingDir, "git-askpass.sh"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS_REQUIRE=force",
	}
}

// HostGitCredentials готовит askpass-файлы на хосте для git-команд раннера.
func HostGitCredentials(baseDir string, grant credentials.Grant) ([]string, func(), error) {
	stagingDir, err := writeAskpassTemp(baseDir, grant, "")
	if err != nil {
		return nil, func() {}, err
	}

	return hostAskpassEnv(stagingDir), func() { os.RemoveAll(stagingDir) }, nil
}

// InstallGitCredentialsForUnit копирует askpass и креденшелы в контейнер
// через docker compose cp (не через argv/env) и возвращает env с путями.
// cleanup удаляет файлы в контейнере и на хосте.
func InstallGitCredentialsForUnit(
	ctx context.Context,
	unitPath string,
	service string,
	grant credentials.Grant,
) ([]string, func(), error) {
	stagingDir, err := writeAskpassTemp(unitPath, grant, containerGitCredPath)
	if err != nil {
		return nil, func() {}, err
	}

	hostAskpass := filepath.Join(stagingDir, "git-askpass.sh")
	hostCred := filepath.Join(stagingDir, "credentials")

	cleanup := func() {
		// Best effort: контейнер мог уже остановиться.
		_, _ = utils.ExecCommandContext(context.Background(), unitPath, []string{
			"docker", "compose", "exec", "-T", service,
			"sh", "-c", "rm -f " + containerAskpassPath + " " + containerGitCredPath,
		})
		os.RemoveAll(stagingDir)
	}

	copies := [][2]string{
		{hostAskpass, service + ":" + containerAskpassPath},
		{hostCred, service + ":" + containerGitCredPath},
	}

	for _, pair := range copies {
		if _, err := utils.ExecCommandContext(ctx, unitPath, []string{"docker", "compose", "cp", pair[0], pair[1]}); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("скопировать git-креденшелы в контейнер: %w", err)
		}
	}

	if _, err := utils.ExecCommandContext(ctx, unitPath, []string{
		"docker", "compose", "exec", "-T", service,
		"sh", "-c", "chmod 700 " + containerAskpassPath + " && chmod 600 " + containerGitCredPath,
	}); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("ограничить права git-креденшелов в контейнере: %w", err)
	}

	env := []string{
		"UNITMAN_GIT_ASKPASS=" + containerAskpassPath,
		"UNITMAN_GIT_TERMINAL_PROMPT=0",
	}

	return env, cleanup, nil
}
