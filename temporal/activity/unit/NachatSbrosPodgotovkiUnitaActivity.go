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

type NachatSbrosPodgotovkiUnita struct {
	UnitId     string
	ProjectId  string
	Name       string
	StorageUrl string
	Commands   []string
	Variables  []model.UnitConfigVariable
}

type ResultatSbrosaPodgotovkiUnita struct {
	ResponseId string
	UnitId     string
	Success    int
	Steps      []model.Step
}

func wrapSbrosPodgotovkiSaveStepToMono(result *ResultatSbrosaPodgotovkiUnita) {
	SaveStepsToMono(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
}

func wrapResultatSbrosaPodgotovki(result *ResultatSbrosaPodgotovkiUnita) *ResultatSbrosaPodgotovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
	return result
}

func wrapResultatSbrosaPodgotovkiSuccess(result *ResultatSbrosaPodgotovkiUnita) *ResultatSbrosaPodgotovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: true})
	return result
}

func NachatSbrosPodgotovkiUnitaActivity(ctx context.Context, command NachatSbrosPodgotovkiUnita) (*ResultatSbrosaPodgotovkiUnita, error) {
	result := &ResultatSbrosaPodgotovkiUnita{
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
		UnitId:     command.UnitId,
		Success:    1,
		Steps:      []model.Step{},
	}

	_, err := json.Marshal(command)
	if err != nil {
		result.Success = 0
		return wrapResultatSbrosaPodgotovki(result), nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	if err != nil {
		result.Success = 0
		return wrapResultatSbrosaPodgotovki(result), nil
	}

	filepath := "./projects/" + command.ProjectId + "/units/" + command.UnitId

	currentPath, err := os.Getwd()
	if err != nil {
		result.Success = 0
		return wrapResultatSbrosaPodgotovki(result), nil
	}

	filepath = currentPath + "/" + filepath

	args := []string{"docker", "compose", "exec", "unit", "sh", "-c", "pwd"}
	unixtimeCheckStart := time.Now().Unix()
	_, err = utils.ExecCommand(filepath, args)

	unitIsRunning := true
	if err != nil {
		if strings.Contains(err.Error(), "no such service") {
			unitIsRunning = false
		}

		if strings.Contains(err.Error(), "is not running") {
			unitIsRunning = false
		}
		result.Steps = model.AddStepToSteps(result.Steps, "check unit", "is not running", nil, unixtimeCheckStart)
	}

	if unitIsRunning == true {
		for _, commandString := range command.Commands {
			unixtimeStart := time.Now().Unix()
			args := []string{"docker", "compose", "exec", "unit", "sh", "-c", commandString}
			msg, errCommand := utils.ExecCommand(filepath, args)
			result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand, unixtimeStart)
			if errCommand != nil {
				result.Success = 0
				return wrapResultatSbrosaPodgotovki(result), nil
			} else {
				wrapSbrosPodgotovkiSaveStepToMono(result)
			}
		}

		args := []string{"docker", "compose", "down", "-v"}
		unixtimeDownStart := time.Now().Unix()
		msg, err := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeDownStart)
		if err != nil {
			result.Success = 0
			return wrapResultatSbrosaPodgotovki(result), nil
		}
	} else {
		result.Steps = model.AddStepToSteps(result.Steps, "check unit", "unit is not running", err, unixtimeCheckStart)
	}

	return wrapResultatSbrosaPodgotovkiSuccess(result), nil
}
