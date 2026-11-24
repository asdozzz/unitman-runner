package unit

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"runner/temporal/utils"
)

// Ленивая сборка проекта на раннере.
//
// Продуктовое правило: отдельной стадии сборки проекта в backend нет. Проект
// собирается на конкретном раннере при первой сборке юнита и факт фиксируется
// в project_build_state. Раскладка остаётся прежней:
//
//	projects/<ProjectId>/mainBranch/ — seed-репозиторий проекта (клон StorageUrl)
//	projects/<ProjectId>/units/<UnitId>/ — рабочий каталог юнита
//
// ensureProjectMainBranch создаёт seed-репозиторий, если его ещё нет. Если он
// уже есть (например, после предыдущей сборки), повторный clone не выполняется —
// это делает retry идемпотентным.
func ensureProjectMainBranch(ctx context.Context, projectDir, storageURL string) (string, string, error) {
	storageURL = strings.TrimSpace(storageURL)
	if storageURL == "" {
		return "", "", errors.New("не задан StorageUrl проекта: невозможно собрать проект на раннере")
	}

	if err := os.MkdirAll(projectDir, os.ModePerm); err != nil {
		return "", "", fmt.Errorf("создать каталог проекта %s: %w", projectDir, err)
	}

	gitDir := filepath.Join(projectDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("проверить seed-репозиторий %s: %w", gitDir, err)
		}

		// Неудачный прошлый clone мог оставить частичные файлы. Чистим каталог
		// и клонируем заново.
		if err := clearDirectory(projectDir); err != nil {
			return "", "", fmt.Errorf("очистить каталог проекта %s: %w", projectDir, err)
		}

		args := []string{"git", "clone", "--progress", storageURL, "."}
		if _, err := utils.ExecCommandContext(ctx, projectDir, args); err != nil {
			// Ошибка уже редактирует credentials в URL; StorageUrl в текст не
			// добавляем, чтобы токен не попал в шаг прогресса.
			return "", "", fmt.Errorf("git clone проекта в %s: %w", projectDir, err)
		}
	}

	commit, err := gitOutput(ctx, projectDir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("определить commit проекта: %w", err)
	}

	branch, err := gitOutput(ctx, projectDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("определить ветку проекта: %w", err)
	}

	// detached HEAD не даёт имени основной ветки — метаданные необязательны.
	if branch == "HEAD" {
		branch = ""
	}

	return commit, branch, nil
}

// gitOutput выполняет git-команду в каталоге репозитория и тримит вывод.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	command := append([]string{"git"}, args...)

	out, err := utils.ExecCommandContext(ctx, dir, command)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// clearDirectory удаляет содержимое каталога, не трогая сам каталог.
func clearDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}

	return nil
}

// sanitizeProjectOrigin удаляет логин/токен из origin seed-репозитория проекта.
// StorageUrl приходит от backend с admin-токеном и используется только на время
// clone/fetch; после синхронизации токен не должен оставаться в .git/config.
func sanitizeProjectOrigin(projectDir string, execCommand func(string, []string) (string, error)) error {
	out, err := execCommand(projectDir, []string{"git", "remote", "get-url", "origin"})
	if err != nil {
		// origin отсутствует — очищать нечего; fetch до этого уже завершился.
		return nil
	}

	clean, changed := stripOriginURLCredentials(strings.TrimSpace(out))
	if !changed {
		return nil
	}

	if _, err := execCommand(projectDir, []string{"git", "remote", "set-url", "origin", clean}); err != nil {
		return fmt.Errorf("удалить учётные данные из origin проекта: %w", err)
	}

	return nil
}

// stripOriginURLCredentials удаляет userinfo из URL и сообщает, изменился ли он.
// Для не-URL строк (например, scp-подобных) возвращает исходное значение.
func stripOriginURLCredentials(raw string) (string, bool) {
	prepared := strings.TrimSpace(raw)
	if prepared == "" {
		return raw, false
	}

	parsed, err := url.Parse(prepared)
	if err != nil || parsed.User == nil {
		return raw, false
	}

	parsed.User = nil
	clean := parsed.String()

	return clean, clean != raw
}
