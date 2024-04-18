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

type NachatZapuskUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	StorageUrl  string
	Commands    []string
	Variables   []model.UnitConfigVariable
}

type ResultatZapuskaUnita struct {
	Success int
	Steps   []model.Step
}

func NachatZapuskUnitaActivity(ctx context.Context, command NachatZapuskUnita) (*ResultatZapuskaUnita, error) {
	result := &ResultatZapuskaUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("ResultatZapuskaUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepath = currentPath + "/" + filepath

	err = os.Setenv("UNITMAN_PROJECT_NAME", command.ProjectName)
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_PROJECT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_UNIT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	for _, variableItem := range command.Variables {
		err = os.Setenv("UNITMAN_"+variableItem.Id, variableItem.Value)
		result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_"+variableItem.Id, "success", err)
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
