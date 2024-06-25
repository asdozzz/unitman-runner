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

type NachatIzmenenieVetkiUnita struct {
	ProjectId  string
	Id         string
	Name       string
	StorageUrl string
	NewBranch  string
}

type ResultatIzmeneniyaVetkiUnita struct {
	Success int
	Steps   []model.Step
	Config  string
}

func NachatIzmeneniyaVetkiUnitaActivity(ctx context.Context, command NachatIzmenenieVetkiUnita) (*ResultatIzmeneniyaVetkiUnita, error) {
	result := &ResultatIzmeneniyaVetkiUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("ResultatPodgotovkiUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}
	appPath := currentPath + "/" + filepath + "/app"

	args := []string{"git", "reset", "--hard"}
	msg, err := utils.ExecCommand(appPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"git", "remote", "set-url", "origin", command.StorageUrl}
	msg, err = utils.ExecCommand(appPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"git", "fetch", "--all"}
	msg, err = utils.ExecCommand(appPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"git", "checkout", "-B", command.NewBranch, "origin/" + command.NewBranch}
	msg, err = utils.ExecCommand(appPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	b, _ := os.ReadFile(appPath + "/unitman.yaml") // just pass the file name
	if b != nil {
		result.Config = string(b)
	}

	return result, nil
}
