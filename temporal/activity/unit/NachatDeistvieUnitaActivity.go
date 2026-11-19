package unit

import (
	"context"
	"encoding/json"
	"os"
	"runner/state"
	"runner/temporal/activity/unit/model"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
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

func NachatDeistvieUnitaActivity(ctx context.Context, command NachatZapuskUnita) (result *ResultatDeistviyaUnita, err error) {
	result = &ResultatDeistviyaUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	defer func() {
		success := false
		if result != nil {
			success = result.Success == 1
		}
		state.RecordActivityResult("deistvie", command.ProjectId, command.Id, command.Name, command.ProjectName, success)
	}()

	_, err = json.Marshal(command)
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
		args := UnitContainerCommand(commandStringAfteReplace)
		msg, errCommand := ExecInUnitContainer(filepath, commandStringAfteReplace)
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
