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

type NachatSbrosPodgotovkiUnita struct {
	UnitId     string
	ProjectId  string
	Name       string
	StorageUrl string
	Commands   []string
	Variables  []model.UnitConfigVariable
}

type ResultatSbrosaPodgotovkiUnita struct {
	UnitId  string
	Success int
	Steps   []model.Step
}

func sendErrorResponse(result *ResultatSbrosaPodgotovkiUnita, err error) (*ResultatSbrosaPodgotovkiUnita, error) {
	result.Success = 0
	return result, nil
}

func NachatSbrosPodgotovkiUnitaActivity(ctx context.Context, command NachatSbrosPodgotovkiUnita) (*ResultatSbrosaPodgotovkiUnita, error) {
	result := &ResultatSbrosaPodgotovkiUnita{
		UnitId:  command.UnitId,
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		return sendErrorResponse(result, err)
	}

	fmt.Println("NachatSbrosPodgotovkiUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.UnitId

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepath = currentPath + "/" + filepath

	args := []string{"docker-compose", "exec", "unit", "sh", "-c", "pwd"}
	_, err = utils.ExecCommand(filepath, args)

	unitIsRunning := true
	if err != nil {
		if strings.Contains(err.Error(), "no such service") {
			unitIsRunning = false
		}

		if strings.Contains(err.Error(), "is not running") {
			unitIsRunning = false
		}
		result.Steps = model.AddStepToSteps(result.Steps, "check unit", "is not running", nil)
	}

	if unitIsRunning == true {
		for _, commandString := range command.Commands {
			args := []string{"docker-compose", "exec", "unit", "sh", "-c", commandString}
			msg, errCommand := utils.ExecCommand(filepath, args)
			result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
			if errCommand != nil {
				result.Success = 0
				return result, nil
			}
		}

		args := []string{"docker-compose", "down", "-v"}
		msg, err := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
		if err != nil {
			result.Success = 0
			return result, nil
		}
	} else {
		result.Steps = model.AddStepToSteps(result.Steps, "check unit", "unit is not running", err)
	}

	return result, nil
}
