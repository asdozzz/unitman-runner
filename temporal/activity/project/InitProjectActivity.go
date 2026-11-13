package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"runner/temporal/activity/project/model"
	"runner/temporal/utils"
	"strings"
	"time"
)

type InitProjectCommand struct {
	ProjectId      string
	MainBranchName string
	StorageUrl     string
}

type InitProjectResult struct {
	Success bool
	Steps   []model.Step
}

func InitProjectActivity(ctx context.Context, command InitProjectCommand) (*InitProjectResult, error) {
	result := &InitProjectResult{
		Success: true,
		Steps:   []model.Step{},
	}

	unixtimeStart := time.Now().Unix()
	out, err := json.Marshal(command)
	if err != nil {
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "json.Marshal", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	}

	fmt.Println("InitProjectCommand:" + string(out))
	filepath := "./projects/" + command.ProjectId + "/mainBranch/"
	unixtimeStart = time.Now().Unix()
	err = os.MkdirAll(filepath, os.ModePerm)
	if err != nil {
		log.Println(err)
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "MkdirAll project dir", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	} else {
		result.Steps = append(result.Steps, model.Step{Command: "MkdirAll project dir", Response: "success", Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
	}

	unixtimeStart = time.Now().Unix()
	dir, err := ioutil.ReadDir(filepath)
	if err != nil {
		log.Println(err)
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: "ReadDir project dir", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	} else {
		result.Steps = append(result.Steps, model.Step{Command: "ReadDir project dir", Response: "success", Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
	}

	for _, d := range dir {
		fmt.Println("clear item:" + path.Join([]string{filepath, d.Name()}...))
		childDir := path.Join([]string{filepath, d.Name()}...)
		unixtimeStart = time.Now().Unix()
		err = os.RemoveAll(childDir)
		if err != nil {
			fmt.Println(err.Error())
			result.Success = false
			result.Steps = append(result.Steps, model.Step{Command: "Remove child item " + childDir, Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
			return result, nil
		} else {
			result.Steps = append(result.Steps, model.Step{Command: "Remove child item " + childDir, Response: "success", Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		}
	}

	unixtimeStart = time.Now().Unix()
	currentPath, err := os.Getwd()
	if err != nil {
		log.Println(err)
		result.Steps = append(result.Steps, model.Step{Command: "Getwd", Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	}

	filepath = currentPath + "/" + filepath
	args := []string{"git", "clone", "--progress", command.StorageUrl, "."}
	argsForResponse := []string{"git", "clone", "--progress", "."}
	unixtimeStart = time.Now().Unix()
	msg, err := utils.ExecCommand(filepath, args)

	if err != nil {
		result.Success = false
		result.Steps = append(result.Steps, model.Step{Command: strings.Join(argsForResponse, " "), Response: err.Error(), Success: false, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
		return result, nil
	} else {
		result.Success = true
		result.Steps = append(result.Steps, model.Step{Command: strings.Join(argsForResponse, " "), Response: msg, Success: true, Unixtime: time.Now().Unix(), UnixtimeStart: unixtimeStart})
	}

	return result, nil
}
