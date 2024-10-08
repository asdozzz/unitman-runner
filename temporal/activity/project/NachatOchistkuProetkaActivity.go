package project

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
)

type NachatOchistkuProekta struct {
	ProjectId   string
	ProjectName string
	StorageUrl  string
}

type ResultatOchistkiProekta struct {
	Success int
	Steps   []model.Step
}

func NachatOchistkuProektaActivity(ctx context.Context, command NachatOchistkuProekta) (*ResultatOchistkiProekta, error) {
	result := &ResultatOchistkiProekta{
		Success: 1,
		Steps:   []model.Step{},
	}

	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("NachatOchistkuProekta:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/clearing"
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

	err = os.Setenv("UNITMAN_PROJECT_NAME", command.ProjectName)
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_PROJECT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", "clearing")
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_UNIT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	envFilePath := filepath + "/.env.unit"

	err = os.Truncate(envFilePath, 0)
	result.Steps = model.AddStepToSteps(result.Steps, "clear env file", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	f, err := os.OpenFile(envFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	result.Steps = model.AddStepToSteps(result.Steps, "open env file", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(f)

	_, err = f.Write([]byte("PODMAN_IGNORE_CGROUPSV1_WARNING=1\n"))
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv PODMAN_IGNORE_CGROUPSV1_WARNING", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	_, err = f.Write([]byte("COMPOSE_PROJECT_NAME=" + "clearing" + "_" + command.ProjectName + "\n"))
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv COMPOSE_PROJECT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args = []string{"docker-compose", "up", "-d", "--build"}
	msg, err = utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	Commands := []string{"podman system prune -a -f"}

	for _, commandString := range Commands {
		//commandArgs := strings.Split(commandString, " ")
		args := []string{"docker-compose", "exec", "unit", "sh", "-c", commandString}
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return result, nil
		}
	}

	args = []string{"docker-compose", "down"}
	msg, err = utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	return result, nil
}
