package unit

import (
	"context"
	"encoding/json"
	"os"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
)

type NachatDeistvieUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	Commands    []string
	Variables   []model.UnitConfigVariable
}

type ResultatDeistviyaUnita struct {
	Success int
	Steps   []model.Step
}

func NachatDeistvieUnitaActivity(ctx context.Context, command NachatZapuskUnita) (*ResultatZapuskaUnita, error) {
	result := &ResultatZapuskaUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	_, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepath = currentPath + "/" + filepath

	for _, commandString := range command.Commands {
		commandStringAfteReplace := commandString
		for _, variableItem := range command.Variables {
			commandStringAfteReplace = strings.Replace(commandStringAfteReplace, "${UNITMAN_ACTION_"+variableItem.Id+"}", variableItem.Value, 1)
		}
		args := []string{"docker-compose", "exec", "unit", "sh", "-c", commandStringAfteReplace}
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
