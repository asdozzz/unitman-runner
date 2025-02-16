package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runner/temporal/utils"
	"strings"
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

func ProveritKonteinerUnitaActivity(ctx context.Context, command ProveritKonteinerUnita) (*ResultatProverkiKonteineraUnita, error) {
	result := &ResultatProverkiKonteineraUnita{
		Success: 1,
	}

	out, err := json.Marshal(command)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	if err != nil {
		result.Success = 1
		return result, nil
	}

	fmt.Println("ProveritKonteinerUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	args := []string{"docker-compose", "exec", "unit", "sh", "-c", "pwd"}
	_, err = utils.ExecCommand(filepath, args)

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
