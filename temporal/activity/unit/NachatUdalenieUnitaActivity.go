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

type NachatUdalenieUnita struct {
	ProjectId  string
	Id         string
	Name       string
	StorageUrl string
}

type ResultatUdaleniyaUnita struct {
	Success int
	Steps   []model.Step
}

func NachatUdalenieUnitaActivity(ctx context.Context, command NachatUdalenieUnita) (*ResultatUdaleniyaUnita, error) {
	result := &ResultatUdaleniyaUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_UNIT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("NachatUdalenieUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	args := []string{"docker-compose", "down"}
	msg, err := utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	err = os.RemoveAll(filepath)
	result.Steps = model.AddStepToSteps(result.Steps, "remove unit directory", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	result.Success = 1
	return result, nil
}
