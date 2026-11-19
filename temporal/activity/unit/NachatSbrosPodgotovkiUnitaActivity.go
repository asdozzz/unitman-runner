package unit

import (
	"context"
	"encoding/json"
	"os"
	"runner/state"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

type NachatSbrosPodgotovkiUnita struct {
	UnitId      string
	ProjectId   string
	ProjectName string
	Name        string
	StorageUrl  string
	Commands    []string
	Variables   []model.UnitConfigVariable
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

func NachatSbrosPodgotovkiUnitaActivity(ctx context.Context, command NachatSbrosPodgotovkiUnita) (result *ResultatSbrosaPodgotovkiUnita, err error) {
	result = &ResultatSbrosaPodgotovkiUnita{
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
		UnitId:     command.UnitId,
		Success:    1,
		Steps:      []model.Step{},
	}

	defer func() {
		success := false
		if result != nil {
			success = result.Success == 1
		}
		state.RecordActivityResult("sbros_podgotovki", command.ProjectId, command.UnitId, command.Name, command.ProjectName, success)
	}()

	_, err = json.Marshal(command)
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

	unixtimeCheckStart := time.Now().Unix()
	_, err = ExecInUnitContainer(filepath, "pwd")

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
			args := UnitContainerCommand(commandString)
			msg, errCommand := ExecInUnitContainer(filepath, commandString)
			result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand, unixtimeStart)
			if errCommand != nil {
				result.Success = 0
				return wrapResultatSbrosaPodgotovki(result), nil
			} else {
				wrapSbrosPodgotovkiSaveStepToMono(result)
			}
		}

		// Тома с состоянием не удаляем: сброс подготовки останавливает
		// контейнеры, но сохраняет /app и данные внутреннего Docker.
		args := []string{"docker", "compose", "down"}
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
