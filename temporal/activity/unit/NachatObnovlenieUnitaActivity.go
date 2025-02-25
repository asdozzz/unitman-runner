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

type NachatObnovlenieUnita struct {
	ProjectId string
	Id        string
	Name      string
}

type ResultatObnovleniyaUnita struct {
	Success int
	Steps   []model.Step
	Config  string
}

func NachatObnovlenieUnitaActivity(ctx context.Context, command NachatObnovlenieUnita) (*ResultatObnovleniyaUnita, error) {
	result := &ResultatObnovleniyaUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("NachatObnovlenieUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepathApp := currentPath + "/" + filepath + "/app"

	args := []string{"git", "pull"}
	msg, err := utils.ExecCommand(filepathApp, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"docker-compose", "exec", "unit", "sh", "-c", "pwd"}
	_, err = utils.ExecCommand(filepath, args)

	unitIsRunning := true
	if err != nil {
		if strings.Contains(err.Error(), "no such service") {
			unitIsRunning = false
		}

		if strings.Contains(err.Error(), "is not running") {
			unitIsRunning = false
		}
	}

	if unitIsRunning {
		args = []string{"docker-compose", "cp", "-a", filepathApp, "unit:/"}
		msg, err := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
		if err != nil {
			result.Success = 0
			return result, nil
		}
	}

	b, _ := os.ReadFile(filepath + "/unitman.yaml") // just pass the file name
	if b != nil {
		result.Config = string(b)
	}

	return result, nil
}
