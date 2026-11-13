package unit

import (
	"context"
	"encoding/json"
	"go.temporal.io/sdk/activity"
	"os"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
)

type NachatOstanovkuUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	StorageUrl  string
	Commands    []string
	Variables   []model.UnitConfigVariable
}

type ResultatOstanovkiUnita struct {
	Success    int
	Steps      []model.Step
	ResponseId string
}

func wrapOstanovkaSaveStepToMono(result *ResultatOstanovkiUnita) {
	SaveStepsToMono(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
}

func wrapResultatOstanovki(result *ResultatOstanovkiUnita) *ResultatOstanovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
	return result
}

func wrapResultatOstanovkiSuccess(result *ResultatOstanovkiUnita) *ResultatOstanovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: true})
	return result
}

func NachatOstanokuUnitaActivity(ctx context.Context, command NachatOstanovkuUnita) (*ResultatOstanovkiUnita, error) {
	result := &ResultatOstanovkiUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	_, err := json.Marshal(command)
	if err != nil {
		result.Success = 0
		return wrapResultatOstanovki(result), nil
	}

	err = os.Setenv("UNITMAN_PROJECT_NAME", command.ProjectName)
	if err != nil {
		result.Success = 0
		return wrapResultatOstanovki(result), nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	if err != nil {
		result.Success = 0
		return wrapResultatOstanovki(result), nil
	}

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Success = 0
		return wrapResultatOstanovki(result), nil
	}

	filepath = currentPath + "/" + filepath

	for _, commandString := range command.Commands {
		args := []string{"docker", "compose", "exec", "unit", "sh", "-c", commandString}
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatOstanovki(result), nil
		} else {
			wrapOstanovkaSaveStepToMono(result)
		}
	}

	result.Success = 1
	return wrapResultatOstanovkiSuccess(result), nil
}
