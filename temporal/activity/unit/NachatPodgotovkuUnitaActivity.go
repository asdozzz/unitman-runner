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

	"go.temporal.io/sdk/activity"
)

type ContainerSettings struct {
	MemoryLimit string
}

type NachatPodgotovkuUnita struct {
	ProjectId         string
	ProjectName       string
	Id                string
	Name              string
	StorageUrl        string
	Commands          []string
	Variables         []model.UnitConfigVariable
	Caches            []Cache
	ContainerSettings ContainerSettings
	Branch            string
	ImageConfig       model.ImageConfigStruct
	Volumes           []Volume
}

type ResultatPodgotovkiUnita struct {
	Success    int
	Steps      []model.Step
	Config     string
	ResponseId string
}

func wrapPodgotovkaSaveStepsToMono(result *ResultatPodgotovkiUnita) {
	SaveStepsToMono(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
}

func wrapResultatPogotovki(result *ResultatPodgotovkiUnita) *ResultatPodgotovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
	return result
}

func wrapResultatPogotovkiSuccess(result *ResultatPodgotovkiUnita) *ResultatPodgotovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: true})
	return result
}

func NachatPodgotovkuUnitaActivity(ctx context.Context, command NachatPodgotovkuUnita) (result *ResultatPodgotovkiUnita, err error) {
	result = &ResultatPodgotovkiUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	defer func() {
		success := false
		if result != nil {
			success = result.Success == 1
		}
		state.RecordActivityResult("podgotovka", command.ProjectId, command.Id, command.Name, command.ProjectName, success)
	}()

	logCommand := command
	logCommand.StorageUrl = RedactStorageURL(command.StorageUrl)

	out, err := json.Marshal(logCommand)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	fmt.Println("ResultatPodgotovkiUnita:" + string(out))

	filepathUnit := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "", err, time.Now().Unix())
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	unitPath := filepath.Join(currentPath, filepathUnit)

	unixtimeStart := time.Now().Unix()
	err = os.MkdirAll(unitPath, os.ModePerm)
	result.Steps = model.AddStepToSteps(result.Steps, "MkdirAll unit dir", unitPath, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	fileSteps, err := PrepareUnitFiles(unitPath, command.ImageConfig)
	result.Steps = append(result.Steps, fileSteps...)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	unixtimeStart = time.Now().Unix()
	err = EnsureUnitSeedDir(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "mkdir unit seed dir", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	volumes := UnitVolumes(command.Id)
	envEntries := [][2]string{
		{"UNITMAN_UNIT_ID", command.Id},
		{"UNITMAN_UNIT_NAME", command.Name},
		{"UNITMAN_PROJECT_NAME", command.ProjectName},
		{"UNITMAN_BRANCH_NAME", command.Branch},
		{"UNITMAN_UNIT_IMAGE_NAME", command.ImageConfig.Image},
		{"COMPOSE_PROJECT_NAME", command.Name + "_" + command.ProjectName},
		{"UNITMAN_UNIT_APP_VOLUME", volumes.App},
		{"UNITMAN_UNIT_DOCKER_VOLUME", volumes.Docker},
		{"UNITMAN_UNIT_CONTAINERS_VOLUME", volumes.Containers},
		{"UNITMAN_MEMORY_LIMIT", command.ContainerSettings.MemoryLimit},
		{"UNITMAN_CACHE_REPO", "host.units:5000"},
	}

	for _, variableItem := range command.Variables {
		envEntries = append(envEntries, [2]string{"UNITMAN_" + variableItem.Id, variableItem.Value})
	}

	unixtimeStart = time.Now().Unix()
	err = WriteUnitEnvFile(unitPath, envEntries)
	result.Steps = model.AddStepToSteps(result.Steps, "write .env", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	volumeSteps, err := EnsureUnitVolumes(unitPath, command.Id)
	result.Steps = append(result.Steps, volumeSteps...)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	args := []string{"docker", "compose", "up", "-d", "--build"}
	unixtimeStart = time.Now().Unix()
	msg, err := utils.ExecCommand(unitPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	unixtimeStart = time.Now().Unix()
	err = WaitUnitReady(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "wait unit ready", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	unixtimeStart = time.Now().Unix()
	msg, err = SyncUnitRepo(unitPath, command.Branch, command.StorageUrl)
	result.Steps = model.AddStepToSteps(result.Steps, "unit-repo-sync branch="+command.Branch, msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	volumeSteps, stagedVolumes, err := PrepareVolumes(unitPath, command.Volumes, func(dir string) (bool, error) {
		return UnitVolumeSeeded(unitPath, dir)
	})
	result.Steps = append(result.Steps, volumeSteps...)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	if stagedVolumes > 0 {
		unixtimeStart = time.Now().Unix()
		msg, err = SyncUnitVolumes(unitPath)
		result.Steps = model.AddStepToSteps(result.Steps, "unit-volumes-sync", msg, err, unixtimeStart)
		if err != nil {
			result.Success = 0
			return wrapResultatPogotovki(result), nil
		}
	}

	unixtimeStart = time.Now().Unix()
	err = RemoveLegacyHostApp(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "remove legacy host app", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	RestoreCache(command.ProjectName, command.Name, command.ProjectId, command.Id, command.Caches)

	for _, commandString := range command.Commands {
		args := UnitContainerCommand(commandString)
		unixtimeStart := time.Now().Unix()
		msg, errCommand := ExecInUnitContainer(unitPath, commandString)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand, unixtimeStart)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatPogotovki(result), nil
		}

		wrapPodgotovkaSaveStepsToMono(result)
	}

	unixtimeStart = time.Now().Unix()
	unitConfig, err := ReadUnitConfig(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "read unitman.yaml", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	result.Config = unitConfig

	return wrapResultatPogotovkiSuccess(result), nil
}
