// Package state хранит ожидаемое и фактическое состояние контейнеров юнитов.
package state

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ExpectedState — ожидаемое состояние контейнера юнита.
type ExpectedState string

const (
	ExpectedRunning ExpectedState = "running"
	ExpectedAbsent  ExpectedState = "absent"
)

// ActualState — фактическое состояние контейнера юнита.
type ActualState string

const (
	ActualRunning ActualState = "running"
	ActualStopped ActualState = "stopped"
	ActualAbsent  ActualState = "absent"
	ActualUnknown ActualState = "unknown"
)

// UnitState — сохранённое состояние одного юнита.
type UnitState struct {
	UnitID             string        `json:"unitId"`
	ProjectID          string        `json:"projectId"`
	UnitName           string        `json:"unitName"`
	ProjectName        string        `json:"projectName"`
	ContainerName      string        `json:"containerName"`
	Expected           ExpectedState `json:"expectedState"`
	ExpectedUpdatedAt  int64         `json:"expectedUpdatedAt"`
	Actual             ActualState   `json:"actualState"`
	ActualCheckedAt    int64         `json:"actualCheckedAt"`
	MismatchSince      int64         `json:"mismatchSince"`
	LastNotifiedActual ActualState   `json:"lastNotifiedActual"`
	LastNotifiedAt     int64         `json:"lastNotifiedAt"`
}

// Store — хранилище состояний юнитов.
type Store interface {
	Get(unitID string) (UnitState, bool, error)
	List() ([]UnitState, error)
	Upsert(UnitState) error
	Delete(unitID string) error
	SetActual(unitID string, actual ActualState, checkedAt int64) error
	MarkNotified(unitID string, actual ActualState, at int64) error
	Close() error
}

// noopStore используется до вызова Init, чтобы активности и тесты не падали.
type noopStore struct{}

func (noopStore) Get(string) (UnitState, bool, error)           { return UnitState{}, false, nil }
func (noopStore) List() ([]UnitState, error)                    { return []UnitState{}, nil }
func (noopStore) Upsert(UnitState) error                        { return nil }
func (noopStore) Delete(string) error                           { return nil }
func (noopStore) SetActual(string, ActualState, int64) error    { return nil }
func (noopStore) MarkNotified(string, ActualState, int64) error { return nil }
func (noopStore) Close() error                                  { return nil }

var (
	globalMu      sync.RWMutex
	globalStore   Store = noopStore{}
	globalPath    string
	globalWorkdir string
)

// Init открывает стор по указанному пути и делает его глобальным.
// Повторный вызов закрывает предыдущий стор.
func Init(path string) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	if _, noop := globalStore.(noopStore); !noop && globalPath == path {
		return nil
	}

	store, err := Open(path)
	if err != nil {
		return err
	}

	old := globalStore
	globalStore = store
	globalPath = path
	// Каталог файла БД — рабочий каталог раннера, в котором лежат
	// projects/<projectID>/units/<unitID>/.env. Для относительного пути
	// filepath.Dir вернёт каталог (в худшем случае ".").
	globalWorkdir = filepath.Dir(path)

	if _, noop := old.(noopStore); !noop {
		if err := old.Close(); err != nil {
			log.Printf("state: close previous store: %v", err)
		}
	}

	return nil
}

// Default возвращает глобальный стор или no-op стор, если Init не вызывался.
func Default() Store {
	globalMu.RLock()
	defer globalMu.RUnlock()

	return globalStore
}

// RecordActivityResult обновляет ожидаемое состояние по результату активности.
func RecordActivityResult(activity, projectID, unitID, unitName, projectName string, success bool) {
	if !success {
		return
	}

	var expected ExpectedState
	switch activity {
	case "podgotovka", "zapusk", "obnovlenie", "deistvie":
		expected = ExpectedRunning
	case "sbros_podgotovki":
		expected = ExpectedAbsent
	case "udalenie":
		Delete(unitID)
		return
	default:
		return
	}

	store := Default()
	current, found, err := store.Get(unitID)
	if err != nil {
		log.Printf("state: get unit %q: %v", unitID, err)
		return
	}

	// Совместимость со старыми payload: у части команд раньше не было
	// ProjectName, поэтому имя проекта (а иногда и юнита) может быть пустым.
	// Пробуем дочитать его из .env юнита.
	if projectName == "" || unitName == "" {
		envUnitName, envProjectName := readUnitNamesFromEnv(projectID, unitID)
		if unitName == "" {
			unitName = envUnitName
		}
		if projectName == "" {
			projectName = envProjectName
		}
	}

	now := time.Now().Unix()

	if !found {
		if unitName == "" || projectName == "" {
			log.Printf(
				"state: skip unit %q (project %q): не удалось определить имя юнита или проекта, запись не создана",
				unitID, projectID,
			)
			return
		}

		current = UnitState{
			UnitID:        unitID,
			ProjectID:     projectID,
			UnitName:      unitName,
			ProjectName:   projectName,
			ContainerName: unitName + "." + projectName,
		}
	} else {
		// Существующую запись обновлять можно, но не затираем известные
		// значения пустыми.
		if current.ProjectID == "" {
			current.ProjectID = projectID
		}
		if unitName != "" {
			current.UnitName = unitName
		}
		if projectName != "" {
			current.ProjectName = projectName
		}
		if current.UnitName != "" && current.ProjectName != "" {
			current.ContainerName = current.UnitName + "." + current.ProjectName
		}
	}

	current.Expected = expected
	current.ExpectedUpdatedAt = now

	if err := store.Upsert(current); err != nil {
		log.Printf("state: upsert unit %q: %v", unitID, err)
	}
}

// readUnitNamesFromEnv читает UNITMAN_UNIT_NAME и UNITMAN_PROJECT_NAME
// из <workdir>/projects/<projectID>/units/<unitID>/.env.
func readUnitNamesFromEnv(projectID, unitID string) (unitName, projectName string) {
	if projectID == "" || unitID == "" {
		return "", ""
	}

	globalMu.RLock()
	workdir := globalWorkdir
	globalMu.RUnlock()

	if workdir == "" {
		workdir = "."
	}

	path := filepath.Join(workdir, "projects", projectID, "units", unitID, ".env")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}

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

		switch strings.TrimSpace(key) {
		case "UNITMAN_UNIT_NAME":
			unitName = trimEnvQuotes(strings.TrimSpace(value))
		case "UNITMAN_PROJECT_NAME":
			projectName = trimEnvQuotes(strings.TrimSpace(value))
		}
	}

	return unitName, projectName
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

// Delete удаляет запись о юните.
func Delete(unitID string) {
	if err := Default().Delete(unitID); err != nil {
		log.Printf("state: delete unit %q: %v", unitID, err)
	}
}

// SetActual обновляет фактическое состояние существующей записи.
func SetActual(unitID string, actual ActualState, checkedAt int64) {
	if err := Default().SetActual(unitID, actual, checkedAt); err != nil {
		log.Printf("state: set actual for unit %q: %v", unitID, err)
	}
}

// MarkNotified фиксирует факт отправки уведомления о расхождении.
func MarkNotified(unitID string, actual ActualState, at int64) {
	if err := Default().MarkNotified(unitID, actual, at); err != nil {
		log.Printf("state: mark notified for unit %q: %v", unitID, err)
	}
}
