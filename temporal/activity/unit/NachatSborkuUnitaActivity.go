package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
)

type NachatSborkuUnita struct {
	ProjectId  string
	Id         string
	Name       string
	Branch     string
	StorageUrl string
}

type ResultatSbrokiUnita struct {
	Success int
	Steps   []model.Step
	Config  string
}

func NachatSborkuUnitaActivity(ctx context.Context, command NachatSborkuUnita) (*ResultatSbrokiUnita, error) {
	result := &ResultatSbrokiUnita{
		Success: 1,
		Steps:   []model.Step{},
		Config:  "",
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("NachatSborkuUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id
	err = os.MkdirAll(filepath, os.ModePerm)
	result.Steps = model.AddStepToSteps(result.Steps, "MkdirAll", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	dir, err := ioutil.ReadDir(filepath)
	result.Steps = model.AddStepToSteps(result.Steps, "ReadDir", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	for _, d := range dir {
		item := path.Join([]string{filepath, d.Name()}...)
		fmt.Println("item:" + item)
		err = os.RemoveAll(item)
		if err != nil {
			result.Steps = model.AddStepToSteps(result.Steps, "remove child item "+item, "success", err)
			result.Success = 0
			return result, nil
		}
	}

	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unitPath := currentPath + "/" + filepath
	dockerFilesPath := currentPath + "/temporal/activity/unit/docker/."
	args := []string{"cp", "-R", dockerFilesPath, unitPath}
	msg, err := utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	appPath := currentPath + "/" + filepath + "/app"
	mainBranchPath := currentPath + "/projects/" + command.ProjectId + "/mainBranch/."
	args = []string{"cp", "-R", mainBranchPath, appPath}
	msg, err = utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"git", "reset", "--hard"}
	msg, err = utils.ExecCommand(appPath, args)
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

	args = []string{"git", "checkout", "-B", command.Branch, "origin/" + command.Branch}
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
