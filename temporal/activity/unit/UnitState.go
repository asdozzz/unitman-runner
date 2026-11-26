package unit

import (
	"fmt"
	"strings"
	"time"

	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
)

const (
	unitAppVolumePrefix    = "unitman_unit_app_"
	unitDockerVolumePrefix = "unitman_unit_docker_"

	// unitReadyTimeout — сколько ждём готовности dockerd и каталога /app
	// после docker compose up.
	unitReadyTimeout = 2 * time.Minute
)

// UnitVolumeSet — external-тома, которые принадлежат конкретному юниту.
// Имена привязаны к UnitId, поэтому не зависят от имени юнита и проекта.
type UnitVolumeSet struct {
	App    string
	Docker string
}

// UnitVolumes возвращает имена томов для указанного UnitId.
func UnitVolumes(unitID string) UnitVolumeSet {
	return UnitVolumeSet{
		App:    unitAppVolumePrefix + unitID,
		Docker: unitDockerVolumePrefix + unitID,
	}
}

// Names возвращает все имена томов юнита.
func (volumes UnitVolumeSet) Names() []string {
	return []string{volumes.App, volumes.Docker}
}

// EnsureUnitVolumes создаёт external-тома юнита, если их ещё нет.
// docker volume create идемпотентен, поэтому вызов безопасен при каждом up.
func EnsureUnitVolumes(unitPath, unitID string) ([]model.Step, error) {
	steps := []model.Step{}

	for _, volumeName := range UnitVolumes(unitID).Names() {
		unixtimeStart := time.Now().Unix()
		args := []string{"docker", "volume", "create", volumeName}
		msg, err := utils.ExecCommand(unitPath, args)
		steps = model.AddStepToSteps(steps, strings.Join(args, " "), strings.TrimSpace(msg), err, unixtimeStart)
		if err != nil {
			return steps, fmt.Errorf("не удалось создать том %s: %w", volumeName, err)
		}
	}

	return steps, nil
}

// RemoveUnitVolumes явно удаляет external-тома юнита.
// Отсутствие тома не считается ошибкой: удаление должно быть идемпотентным.
func RemoveUnitVolumes(unitPath, unitID string) ([]model.Step, error) {
	steps := []model.Step{}

	for _, volumeName := range UnitVolumes(unitID).Names() {
		unixtimeStart := time.Now().Unix()
		args := []string{"docker", "volume", "rm", "-f", volumeName}
		msg, err := utils.ExecCommand(unitPath, args)
		if err != nil && isMissingVolumeError(err) {
			err = nil
			msg = "том уже отсутствует"
		}
		steps = model.AddStepToSteps(steps, strings.Join(args, " "), strings.TrimSpace(msg), err, unixtimeStart)
		if err != nil {
			return steps, fmt.Errorf("не удалось удалить том %s: %w", volumeName, err)
		}
	}

	return steps, nil
}

// RemoveLegacyUnitVolumes удаляет тома старой схемы, которые были привязаны
// к COMPOSE_PROJECT_NAME. Ошибки отсутствия тома игнорируются.
func RemoveLegacyUnitVolumes(workingDir, composeProjectName string) []model.Step {
	steps := []model.Step{}

	preparedProjectName := strings.TrimSpace(composeProjectName)
	if preparedProjectName == "" {
		return steps
	}

	legacySuffixes := []string{
		"_unit_app",
		"_unitman_unit_app",
		"_unit_docker_data",
		"_unitman_unit_docker_data",
		"_unit_containers",
		"_unitman_unit_containers",
	}

	for _, suffix := range legacySuffixes {
		volumeName := preparedProjectName + suffix
		unixtimeStart := time.Now().Unix()
		args := []string{"docker", "volume", "rm", "-f", volumeName}
		msg, err := utils.ExecCommand(workingDir, args)
		if err != nil && isMissingVolumeError(err) {
			err = nil
			msg = "том уже отсутствует"
		}
		steps = model.AddStepToSteps(steps, strings.Join(args, " "), strings.TrimSpace(msg), err, unixtimeStart)
	}

	return steps
}

// WaitUnitReady ждёт, пока entrypoint юнита подготовит /app и dockerd.
func WaitUnitReady(unitPath string) error {
	deadline := time.Now().Add(unitReadyTimeout)
	lastErr := fmt.Errorf("юнит ещё не готов")

	for {
		_, err := ExecInUnitContainer(unitPath, "test -f /tmp/.app_ready && test -S /var/run/docker.sock && docker info >/dev/null 2>&1")
		if err == nil {
			return nil
		}

		lastErr = err

		if time.Now().After(deadline) {
			return fmt.Errorf("юнит не стал готов за %s: %w", unitReadyTimeout, lastErr)
		}

		time.Sleep(time.Second)
	}
}

func isMissingVolumeError(err error) bool {
	message := strings.ToLower(err.Error())

	return strings.Contains(message, "no such volume") ||
		strings.Contains(message, "not found")
}
