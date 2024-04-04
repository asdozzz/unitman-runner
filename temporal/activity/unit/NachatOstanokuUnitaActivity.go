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

type NachatOstanovkuUnita struct {
	ProjectId   string
	ProjectName string
	Name        string
	Commands    []string
	Variables   []model.UnitConfigVariable
}

type ResultatOstanovkiUnita struct {
	Success int
	Steps   []model.Step
}

func NachatOstanokuUnitaActivity(ctx context.Context, command NachatOstanovkuUnita) (*ResultatOstanovkiUnita, error) {
	result := &ResultatOstanovkiUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("ResultatOstanovkiUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Name

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepath = currentPath + "/" + filepath

	err = os.Setenv("PROJECT_NAME", command.ProjectName)
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv PROJECT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	err = os.Setenv("UNITNAME", command.Name)
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITNAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

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
		result.Steps = model.AddStepToSteps(result.Steps, commandString, msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return result, nil
		}
	}

	result.Success = 1
	return result, nil
}
