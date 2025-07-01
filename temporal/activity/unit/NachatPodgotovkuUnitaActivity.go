package unit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
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
	Caches      []Cache
}

type ResultatPodgotovkiUnita struct {
	Success    int
	Steps      []model.Step
	Config     string
	ResponseId string
}

func wrapResultatPogotovki(result *ResultatPodgotovkiUnita) *ResultatPodgotovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps})
	return result
}

func NachatPodgotovkuUnitaActivity(ctx context.Context, command NachatPodgotovkuUnita) (*ResultatPodgotovkiUnita, error) {
	result := &ResultatPodgotovkiUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: uuid.New().String(),
	}

	out, err := json.Marshal(command)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	fmt.Println("ResultatPodgotovkiUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	filepath = currentPath + "/" + filepath

	err = os.Setenv("UNITMAN_PROJECT_NAME", command.ProjectName)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	envFilePath := filepath + "/.env"

	if _, err := os.Stat(envFilePath); errors.Is(err, os.ErrNotExist) {
		envFilePath = filepath + "/.env.unit"
	}

	err = os.Truncate(envFilePath, 0)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	f, err := os.OpenFile(envFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(f)

	_, err = f.Write([]byte("PODMAN_IGNORE_CGROUPSV1_WARNING=1\n"))
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	_, err = f.Write([]byte("UNITMAN_UNIT_NAME=" + command.Name + "\n"))
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	_, err = f.Write([]byte("UNITMAN_PROJECT_NAME=" + command.ProjectName + "\n"))
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	_, err = f.Write([]byte("COMPOSE_PROJECT_NAME=" + command.Name + "_" + command.ProjectName + "\n"))
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	for _, variableItem := range command.Variables {
		_, err = f.Write([]byte("UNITMAN_" + variableItem.Id + "=" + variableItem.Value + "\n"))
		result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_"+variableItem.Id, "success", err)
		if err != nil {
			result.Success = 0
			return wrapResultatPogotovki(result), nil
		}
	}

	args := []string{"docker-compose", "up", "-d", "--build"}
	msg, err := utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	RestoreCache(command.ProjectName, command.Name, command.ProjectId, command.Id, command.Caches)

	for _, commandString := range command.Commands {
		//commandArgs := strings.Split(commandString, " ")
		args := []string{"docker-compose", "exec", "unit", "sh", "-c", commandString}
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatPogotovki(result), nil
		}
	}

	return wrapResultatPogotovki(result), nil
}
