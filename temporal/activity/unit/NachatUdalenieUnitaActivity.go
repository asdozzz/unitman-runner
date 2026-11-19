package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runner/state"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
	"time"
)

type NachatUdalenieUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	StorageUrl  string
}

type ResultatUdaleniyaUnita struct {
	Success int
	Steps   []model.Step
}

func NachatUdalenieUnitaActivity(ctx context.Context, command NachatUdalenieUnita) (result *ResultatUdaleniyaUnita, err error) {
	result = &ResultatUdaleniyaUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	defer func() {
		success := false
		if result != nil {
			success = result.Success == 1
		}
		state.RecordActivityResult("udalenie", command.ProjectId, command.Id, command.Name, command.ProjectName, success)
		if success {
			state.Delete(command.Id)
		}
	}()

	logCommand := command
	logCommand.StorageUrl = RedactStorageURL(command.StorageUrl)

	unixtimeStart := time.Now().Unix()
	out, err := json.Marshal(logCommand)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("NachatUdalenieUnita:" + string(out))

	filepathUnit := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "", err, time.Now().Unix())
		result.Success = 0
		return result, nil
	}

	unitPath := filepath.Join(currentPath, filepathUnit)
	composeProjectName := ReadUnitEnvValue(unitPath, "COMPOSE_PROJECT_NAME")

	unixtimeStart = time.Now().Unix()
	unitDirInfo, err := os.Stat(unitPath)
	unitDirExists := err == nil && unitDirInfo.IsDir()
	if err != nil && !os.IsNotExist(err) {
		result.Steps = model.AddStepToSteps(result.Steps, "check unit dir", "", err, unixtimeStart)
		result.Success = 0
		return result, nil
	}
	result.Steps = model.AddStepToSteps(result.Steps, "check unit dir", "", nil, unixtimeStart)

	if unitDirExists {
		composePath := filepath.Join(unitPath, "compose.yaml")
		envPath := filepath.Join(unitPath, ".env")

		if _, errCompose := os.Stat(composePath); errCompose == nil {
			if _, errEnv := os.Stat(envPath); errEnv == nil {
				args := []string{"docker", "compose", "down", "-v"}
				unixtimeStart = time.Now().Unix()
				msg, errDown := utils.ExecCommand(unitPath, args)
				result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errDown, unixtimeStart)
				if errDown != nil {
					result.Success = 0
					return result, nil
				}
			}
		}
	}

	volumeSteps, err := RemoveUnitVolumes(currentPath, command.Id)
	result.Steps = append(result.Steps, volumeSteps...)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	result.Steps = append(result.Steps, RemoveLegacyUnitVolumes(currentPath, composeProjectName)...)

	unixtimeStart = time.Now().Unix()
	err = os.RemoveAll(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "remove unit directory", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	result.Success = 1
	return result, nil
}
