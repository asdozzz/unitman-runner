package unit

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
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
	Success    int
	Steps      []model.Step
	ResponseId string
}

func wrapResultatDeistvie(result *ResultatDeistviyaUnita) *ResultatDeistviyaUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps})
	return result
}

func NachatDeistvieUnitaActivity(ctx context.Context, command NachatZapuskUnita) (*ResultatDeistviyaUnita, error) {
	result := &ResultatDeistviyaUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: uuid.New().String(),
	}

	_, err := json.Marshal(command)
	if err != nil {
		result.Success = 0
		return wrapResultatDeistvie(result), nil
	}

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Success = 0
		return wrapResultatDeistvie(result), nil
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
			return wrapResultatDeistvie(result), nil
		}
	}

	result.Success = 1
	return wrapResultatDeistvie(result), nil
}
