package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
)

type NachatPodgotovkuUnita struct {
	ProjectId  string
	Id         string
	Name       string
	StorageUrl string
	Commands   []string
	Variables  []model.UnitConfigVariable
}

type ResultatPodgotovkiUnita struct {
	Success int
	Steps   []model.Step
	Config  string
}

func NachatPodgotovkuUnitaActivity(ctx context.Context, command NachatPodgotovkuUnita) (*ResultatPodgotovkiUnita, error) {
	result := &ResultatPodgotovkiUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("ResultatPodgotovkiUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepath = currentPath + "/" + filepath

	for _, variableItem := range command.Variables {
		err = os.Setenv(variableItem.Id, variableItem.Value)
		result.Steps = model.AddStepToSteps(result.Steps, "Setenv "+variableItem.Id, "success", err)
		if err != nil {
			result.Success = 0
			return result, nil
		}
	}

	for _, commandString := range command.Commands {
		args := strings.Split(commandString, " ")
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, commandString, msg, err)
		if errCommand != nil {
			result.Success = 0
			return result, nil
		}
	}

	return result, nil
}
