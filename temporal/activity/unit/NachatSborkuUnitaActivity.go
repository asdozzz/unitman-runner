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
	"time"
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

	unixtimeStart := time.Now().Unix()
	_, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id
	unixtimeStart = time.Now().Unix()
	err = os.MkdirAll(filepath, os.ModePerm)
	result.Steps = model.AddStepToSteps(result.Steps, "MkdirAll", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	dir, err := ioutil.ReadDir(filepath)
	result.Steps = model.AddStepToSteps(result.Steps, "ReadDir", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	for _, d := range dir {
		item := path.Join([]string{filepath, d.Name()}...)
		fmt.Println("item:" + item)
		unixtimeStart = time.Now().Unix()
		err = os.RemoveAll(item)
		if err != nil {
			result.Steps = model.AddStepToSteps(result.Steps, "remove child item "+item, "success", err, unixtimeStart)
			result.Success = 0
			return result, nil
		}
	}

	unixtimeStart = time.Now().Unix()
	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unitPath := currentPath + "/" + filepath
	dockerFilesPath := currentPath + "/temporal/activity/unit/docker-dind/."
	args := []string{"cp", "-R", dockerFilesPath, unitPath}
	unixtimeStart = time.Now().Unix()
	msg, err := utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	appPath := currentPath + "/" + filepath + "/app"
	mainBranchPath := currentPath + "/projects/" + command.ProjectId + "/mainBranch/."
	args = []string{"cp", "-R", mainBranchPath, appPath}
	unixtimeStart = time.Now().Unix()
	msg, err = utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"git", "reset", "--hard"}
	unixtimeStart = time.Now().Unix()
	msg, err = utils.ExecCommand(appPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"git", "remote", "set-url", "origin", command.StorageUrl}
	unixtimeStart = time.Now().Unix()
	msg, err = utils.ExecCommand(appPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"git", "fetch", "--all"}
	unixtimeStart = time.Now().Unix()
	msg, err = utils.ExecCommand(appPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"git", "checkout", "-B", command.Branch, "origin/" + command.Branch}
	unixtimeStart = time.Now().Unix()
	msg, err = utils.ExecCommand(appPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
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
