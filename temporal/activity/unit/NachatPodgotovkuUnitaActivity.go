package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
)

type NachatPodgotovkuUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	StorageUrl  string
	Commands    []string
	Variables   []model.UnitConfigVariable
}

type ResultatPodgotovkiUnita struct {
	Success int
	Steps   []model.Step
	Config  string
}

func NachatPodgotovkuUnitaActivity(ctx context.Context, command NachatPodgotovkuUnita) (*ResultatPodgotovkiUnita, error) {
	result := &ResultatPodgotovkiUnita{
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

	filepath = currentPath + "/" + filepath

	err = os.Setenv("UNITMAN_PROJECT_NAME", command.ProjectName)
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_PROJECT_NAME", "success", err)
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

	_, err = f.Write([]byte("DOCKER_HOST=unix:////var/run/user/1000/docker.sock\n"))
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_UNIT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	_, err = f.Write([]byte("UNITMAN_UNIT_NAME=" + command.Name + "\n"))
	result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_UNIT_NAME", "success", err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	for _, variableItem := range command.Variables {
		_, err = f.Write([]byte("UNITMAN_" + variableItem.Id + "=" + variableItem.Value + "\n"))
		result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_"+variableItem.Id, "success", err)
		if err != nil {
			result.Success = 0
			return result, nil
		}
	}

	args := []string{"docker-compose", "up", "-d", "--build"}
	msg, err := utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	execDockerCommand := []string{"docker-compose", "exec", "unit"}

	for _, commandString := range command.Commands {
		commandArgs := strings.Split(commandString, " ")
		args := append(execDockerCommand, commandArgs...)
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return result, nil
		}
	}

	return result, nil
}
