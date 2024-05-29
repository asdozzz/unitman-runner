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

	args := []string{"echo", "\"UNITMAN_UNIT_NAME=" + command.Name + "\"", ">", ".env.unit"}
	msg, errCommand := utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
	if errCommand != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"echo", "\"UNITMAN_PROJECT_NAME=" + command.ProjectName + "\"", ">>", ".env.unit"}
	msg, errCommand = utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
	if errCommand != nil {
		result.Success = 0
		return result, nil
	}

	for _, variableItem := range command.Variables {
		args := []string{"echo", "\"UNITMAN_" + variableItem.Id + "=" + variableItem.Value + "\"", ">>", ".env.unit"}
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return result, nil
		}
	}

	execDockerCommand := []string{"docker-compose", "exec", "unit"}

	for _, commandString := range command.Commands {
		commandArgs := strings.Split(commandString, " ")
		args := append(execDockerCommand, commandArgs...)
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return result, nil
		}
	}

	result.Success = 1
	return result, nil
}
