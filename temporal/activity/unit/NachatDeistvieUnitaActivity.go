package unit

import (
	"context"
	"encoding/json"
	"go.temporal.io/sdk/activity"
	"os"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
	"time"
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

func wrapDeistvieSaveStepToMono(result *ResultatDeistviyaUnita) {
	SaveStepsToMono(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
}

func wrapResultatDeistvie(result *ResultatDeistviyaUnita) *ResultatDeistviyaUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
	return result
}

func wrapResultatDeistvieSuccess(result *ResultatDeistviyaUnita) *ResultatDeistviyaUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: true})
	return result
}

func NachatDeistvieUnitaActivity(ctx context.Context, command NachatZapuskUnita) (*ResultatDeistviyaUnita, error) {
	result := &ResultatDeistviyaUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
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
		unixtimeStart := time.Now().Unix()
		commandStringAfteReplace := commandString
		for _, variableItem := range command.Variables {
			commandStringAfteReplace = strings.Replace(commandStringAfteReplace, "${UNITMAN_ACTION_"+variableItem.Id+"}", variableItem.Value, 1)
		}
		args := []string{"docker", "compose", "exec", "unit", "sh", "-c", commandStringAfteReplace}
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand, unixtimeStart)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatDeistvie(result), nil
		} else {
			wrapDeistvieSaveStepToMono(result)
		}
	}

	result.Success = 1
	return wrapResultatDeistvieSuccess(result), nil
}
