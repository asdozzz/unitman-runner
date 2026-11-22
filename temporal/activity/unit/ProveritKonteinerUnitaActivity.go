package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"runner/state"
	"strings"
	"time"
)

type ProveritKonteinerUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
}

type ResultatProverkiKonteineraUnita struct {
	Success int
}

func ProveritKonteinerUnitaActivity(ctx context.Context, command ProveritKonteinerUnita) (result *ResultatProverkiKonteineraUnita, err error) {
	result = &ResultatProverkiKonteineraUnita{
		Success: 1,
	}

	defer func() {
		success := false
		if result != nil {
			success = result.Success == 1
		}
		state.RecordActivityResult("proverka_konteinera", command.ProjectId, command.Id, command.Name, command.ProjectName, success)
		if result != nil && result.Success == 1 {
			state.SetActual(command.Id, state.ActualRunning, time.Now().Unix())
		} else {
			state.SetActual(command.Id, state.ActualAbsent, time.Now().Unix())
		}
	}()

	out, err := json.Marshal(command)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	// Имена юнита и проекта для compose-интерполяции берутся из .env рабочего
	// каталога: мутация окружения процесса протекала между активностями
	// и разворачивала контейнеры с чужими именами.
	fmt.Println("ProveritKonteinerUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	_, err = ExecInUnitContainer(filepath, "pwd")

	if err != nil {
		if strings.Contains(err.Error(), "no such service") {
			result.Success = 0
		}

		if strings.Contains(err.Error(), "is not running") {
			result.Success = 0
		}
	}

	return result, nil
}
