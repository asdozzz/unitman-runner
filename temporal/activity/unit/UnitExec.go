package unit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"runner/temporal/utils"
)

const unitComposeService = "unit"

// UnitExecOptions — дополнительные параметры запуска команды в контейнере.
// WorkDir задаёт рабочий каталог внутри контейнера, Env — переменные
// окружения в формате KEY=VALUE, Service — имя compose-сервиса.
type UnitExecOptions struct {
	WorkDir string
	Env     []string
	// Service — имя compose-сервиса. Пустое значение означает сервис юнита
	// "unit"; workspace-ходы указывают "workspace".
	Service string
}

// UnitContainerCommand собирает argv для запуска shell-команды внутри
// контейнера юнита. Все команды активностей идут через один compose-сервис
// "unit", поэтому argv удобно переиспользовать в шагах прогресса.
func UnitContainerCommand(command string) []string {
	return UnitContainerCommandWithOptions(command, UnitExecOptions{})
}

// UnitContainerCommandWithOptions собирает argv с рабочим каталогом,
// переменными окружения и именем compose-сервиса внутри контейнера.
func UnitContainerCommandWithOptions(command string, opts UnitExecOptions) []string {
	args := []string{"docker", "compose", "exec", "-T"}

	if workDir := strings.TrimSpace(opts.WorkDir); workDir != "" {
		args = append(args, "--workdir", workDir)
	}
	for _, env := range opts.Env {
		if strings.TrimSpace(env) == "" {
			continue
		}
		args = append(args, "-e", env)
	}

	service := strings.TrimSpace(opts.Service)
	if service == "" {
		service = unitComposeService
	}

	return append(args, service, "sh", "-c", command)
}

// ExecInUnitContainer выполняет команду внутри работающего контейнера юнита.
// unitPath — каталог с compose.yaml, из которого compose находит сервис unit.
func ExecInUnitContainer(unitPath, command string) (string, error) {
	return execInUnitContainerWithOptions(unitPath, command, UnitExecOptions{}, utils.ExecCommand)
}

// ExecInUnitContainerWithOptions выполняет команду с рабочим каталогом и
// переменными окружения внутри контейнера юнита.
func ExecInUnitContainerWithOptions(unitPath, command string, opts UnitExecOptions) (string, error) {
	return execInUnitContainerWithOptions(unitPath, command, opts, utils.ExecCommand)
}

// ExecInUnitContainerContext выполняет команду в контейнере юнита с привязкой
// к контексту: отмена контекста завершает docker compose exec.
func ExecInUnitContainerContext(ctx context.Context, unitPath, command string, opts UnitExecOptions) (string, error) {
	if err := validateUnitExec(unitPath, command); err != nil {
		return "", err
	}

	return utils.ExecCommandContext(ctx, unitPath, UnitContainerCommandWithOptions(command, opts))
}

func execInUnitContainer(unitPath, command string, exec func(string, []string) (string, error)) (string, error) {
	return execInUnitContainerWithOptions(unitPath, command, UnitExecOptions{}, exec)
}

func execInUnitContainerWithOptions(
	unitPath, command string,
	opts UnitExecOptions,
	exec func(string, []string) (string, error),
) (string, error) {
	if err := validateUnitExec(unitPath, command); err != nil {
		return "", err
	}

	return exec(unitPath, UnitContainerCommandWithOptions(command, opts))
}

func validateUnitExec(unitPath, command string) error {
	if strings.TrimSpace(unitPath) == "" {
		return errors.New("не задан каталог юнита")
	}
	if strings.TrimSpace(command) == "" {
		return errors.New("команда для контейнера юнита пуста")
	}
	return nil
}

// UnitDirectory возвращает абсолютный путь к каталогу юнита по раскладке
// раннера: <workdir>/projects/<ProjectId>/units/<UnitId>.
func UnitDirectory(projectID, unitID string) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("определить рабочий каталог раннера: %w", err)
	}

	return unitDirectory(root, projectID, unitID)
}

func unitDirectory(root, projectID, unitID string) (string, error) {
	if !isUnitPathSegment(projectID) {
		return "", fmt.Errorf("некорректный ProjectId %q", projectID)
	}
	if !isUnitPathSegment(unitID) {
		return "", fmt.Errorf("некорректный UnitId %q", unitID)
	}

	if strings.TrimSpace(root) == "" {
		root = "."
	}

	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("определить корень раскладки юнитов: %w", err)
	}

	dir := filepath.Join(absolute, "projects", projectID, "units", unitID)
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("каталог юнита не найден: %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("каталог юнита не является каталогом: %s", dir)
	}

	return dir, nil
}

// RunUnitCommand находит каталог юнита и выполняет команду в его контейнере.
func RunUnitCommand(projectID, unitID, command string) (string, error) {
	dir, err := UnitDirectory(projectID, unitID)
	if err != nil {
		return "", err
	}

	return ExecInUnitContainer(dir, command)
}

// isUnitPathSegment пропускает только один нормальный сегмент пути, чтобы
// ProjectId/UnitId из внешнего запроса не могли выйти за пределы projects/.
func isUnitPathSegment(segment string) bool {
	switch segment {
	case "", ".", "..":
		return false
	}

	if strings.ContainsAny(segment, `/\`) {
		return false
	}

	return filepath.Base(segment) == segment
}
