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
	UnitId    string
	ProjectId string
	Name      string
	Commands  []string
	Variables []model.UnitConfigVariable
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

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Name

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepath = currentPath + "/" + filepath

	for _, variableItem := range command.Variables {
		err = os.Setenv(variableItem.Id, variableItem.Value)
		result.Steps = model.AddStepToSteps(result.Steps, "Setenv "+variableItem.Id, "success", err)
		if err != nil {
			return sendErrorResponse(result, err)
		}
	}

	for _, commandString := range command.Commands {
		args := strings.Split(commandString, " ")
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, commandString, msg, errCommand)
		if errCommand != nil {
			return sendErrorResponse(result, errCommand)
		}
	}

	return result, nil
}
