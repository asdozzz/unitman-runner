package monitor

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"runner/state"
)

const (
	unitIDEnvKey      = "UNITMAN_UNIT_ID"
	unitNameEnvKey    = "UNITMAN_UNIT_NAME"
	projectNameEnvKey = "UNITMAN_PROJECT_NAME"
)

// Bootstrap создаёт записи состояния для уже существующих юнитов.
// Ожидаемое состояние берётся равным фактическому, чтобы не рассылать
// ложные уведомления о расхождениях сразу после старта раннера.
func Bootstrap(ctx context.Context, store state.Store, workdir string) {
	actualMap, err := readActualState(ctx, workdir)
	if err != nil {
		log.Printf("monitor: bootstrap: %v", err)
		return
	}

	projectsRoot := filepath.Join(workdir, "projects")
	projects, err := os.ReadDir(projectsRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("monitor: bootstrap: read %q: %v", projectsRoot, err)
		}
		return
	}

	now := time.Now().Unix()

	for _, project := range projects {
		if !project.IsDir() {
			continue
		}

		unitsRoot := filepath.Join(projectsRoot, project.Name(), "units")
		units, err := os.ReadDir(unitsRoot)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("monitor: bootstrap: read %q: %v", unitsRoot, err)
			}
			continue
		}

		for _, unitEntry := range units {
			if !unitEntry.IsDir() {
				continue
			}

			unitPath := filepath.Join(unitsRoot, unitEntry.Name())
			if !hasFile(filepath.Join(unitPath, "compose.yaml")) || !hasFile(filepath.Join(unitPath, ".env")) {
				continue
			}

			env, err := readEnvFile(filepath.Join(unitPath, ".env"))
			if err != nil {
				log.Printf("monitor: bootstrap: read env %q: %v", unitPath, err)
				continue
			}

			unitID := env[unitIDEnvKey]
			if unitID == "" {
				log.Printf("monitor: bootstrap: пропуск %q: пустой %s", unitPath, unitIDEnvKey)
				continue
			}

			if _, found, err := store.Get(unitID); err != nil {
				log.Printf("monitor: bootstrap: get unit %q: %v", unitID, err)
				continue
			} else if found {
				continue
			}

			unitName := env[unitNameEnvKey]
			projectName := env[projectNameEnvKey]
			if unitName == "" || projectName == "" {
				log.Printf("monitor: bootstrap: пропуск %q: нет имени юнита или проекта", unitPath)
				continue
			}

			containerName := unitName + "." + projectName
			actual := actualForContainer(containerName, actualMap)
			if actual != state.ActualRunning && actual != state.ActualAbsent {
				continue
			}

			expected := state.ExpectedAbsent
			if actual == state.ActualRunning {
				expected = state.ExpectedRunning
			}

			item := state.UnitState{
				UnitID:            unitID,
				ProjectID:         project.Name(),
				UnitName:          unitName,
				ProjectName:       projectName,
				ContainerName:     containerName,
				Expected:          expected,
				ExpectedUpdatedAt: now,
				Actual:            actual,
				ActualCheckedAt:   now,
			}

			if err := store.Upsert(item); err != nil {
				log.Printf("monitor: bootstrap: upsert unit %q: %v", unitID, err)
				continue
			}

			log.Printf("monitor: bootstrap: создана запись unit %q (expected=%s)", unitID, expected)
		}
	}
}

// hasFile проверяет существование обычного файла.
func hasFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return !info.IsDir()
}

// readEnvFile разбирает файл .env в карту переменных окружения.
// Семантика совпадает с unit.ReadEnvFile: пустые строки и комментарии
// пропускаются, значение берётся после первого "=", кавычки снимаются.
func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		result[key] = trimEnvQuotes(strings.TrimSpace(value))
	}

	return result, nil
}

// trimEnvQuotes снимает парные одинарные или двойные кавычки.
func trimEnvQuotes(value string) string {
	if len(value) < 2 {
		return value
	}

	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}

	return value
}
