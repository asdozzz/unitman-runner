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

type NachatZapuskUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	StorageUrl  string
	Commands    []string
	Variables   []model.UnitConfigVariable
	Caches      []Cache
	Branch      string
}

type ResultatZapuskaUnita struct {
	Success    int
	Steps      []model.Step
	ResponseId string
}

func wrapZapuskaToMono(result *ResultatZapuskaUnita) {
	SaveStepsToMono(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
}

func wrapResultatZapuska(result *ResultatZapuskaUnita) *ResultatZapuskaUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
	return result
}

func wrapResultatZapuskaSuccess(result *ResultatZapuskaUnita) *ResultatZapuskaUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: true})
	return result
}

func NachatZapuskUnitaActivity(ctx context.Context, command NachatZapuskUnita) (*ResultatZapuskaUnita, error) {
	result := &ResultatZapuskaUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	_, err := json.Marshal(command)
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	err = os.Setenv("UNITMAN_PROJECT_NAME", command.ProjectName)
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	err = os.Setenv("UNITMAN_BRANCH_NAME", command.Branch)
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Success = 0
		return wrapResultatZapuska(result), nil
	}

	filepath = currentPath + "/" + filepath

	for _, commandString := range command.Commands {
		args := []string{"docker", "compose", "exec", "unit", "sh", "-c", commandString}
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatZapuska(result), nil
		} else {
			wrapZapuskaToMono(result)
		}
	}

	go MakeCache(command.ProjectName, command.Name, command.ProjectId, command.Id, command.Caches)

	result.Success = 1
	return wrapResultatZapuskaSuccess(result), nil
}
