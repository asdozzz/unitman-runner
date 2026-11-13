package unit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.temporal.io/sdk/activity"
	"log"
	"os"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"strings"
	"text/template"
	"time"
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

func NachatPodgotovkuUnitaActivity(ctx context.Context, command NachatPodgotovkuUnita) (*ResultatPodgotovkiUnita, error) {
	result := &ResultatPodgotovkiUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	out, err := json.Marshal(command)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	fmt.Println("ResultatPodgotovkiUnita:" + string(out))

	filepath := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	filepath = currentPath + "/" + filepath

	err = os.Setenv("UNITMAN_PROJECT_NAME", command.ProjectName)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	err = os.Setenv("UNITMAN_UNIT_NAME", command.Name)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	err = os.Setenv("UNITMAN_BRANCH_NAME", command.Branch)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	err = os.Setenv("UNITMAN_UNIT_IMAGE_NAME", command.ImageConfig.Image)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	err = os.Setenv("UNITMAN_CACHE_REPO", "host.units:5000")
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	err = os.Setenv("UNITMAN_MEMORY_LIMIT", command.ContainerSettings.MemoryLimit)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	err = os.Setenv("PODMAN_COMPOSE_IN_POD", "false")
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	envFilePath := filepath + "/.env"

	if _, err := os.Stat(envFilePath); errors.Is(err, os.ErrNotExist) {
		envFilePath = filepath + "/.env.unit"
	}

	err = os.Truncate(envFilePath, 0)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	f, err := os.OpenFile(envFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(f)

	_, err = f.Write([]byte("UNITMAN_UNIT_NAME=" + command.Name + "\n"))
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	_, err = f.Write([]byte("UNITMAN_PROJECT_NAME=" + command.ProjectName + "\n"))
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	_, err = f.Write([]byte("UNITMAN_BRANCH_NAME=" + command.Branch + "\n"))
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	_, err = f.Write([]byte("UNITMAN_UNIT_IMAGE_NAME=" + command.ImageConfig.Image + "\n"))
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	_, err = f.Write([]byte("COMPOSE_PROJECT_NAME=" + command.Name + "_" + command.ProjectName + "\n"))
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	_, err = f.Write([]byte("PODMAN_COMPOSE_IN_POD=false" + "\n"))
	if err != nil {
		fmt.Println(err)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	for _, variableItem := range command.Variables {
		unixtimeStart := time.Now().Unix()
		_, err = f.Write([]byte("UNITMAN_" + variableItem.Id + "=" + variableItem.Value + "\n"))
		result.Steps = model.AddStepToSteps(result.Steps, "Setenv UNITMAN_"+variableItem.Id, "success", err, unixtimeStart)
		if err != nil {
			fmt.Println(err)
			result.Success = 0
			return wrapResultatPogotovki(result), nil
		}
	}

	unixtimeStart := time.Now().Unix()
	readyTemplate, err := template.ParseFiles(filepath + "/Dockerfile.temp")

	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "parse Dockerfile template", "fail", err, unixtimeStart)
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	outputFile, err := os.Create(filepath + "/Dockerfile")

	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "make Dockerfile", "fail", err, unixtimeStart)
		result.Success = 0
		return result, nil
	}
	defer outputFile.Close()

	unixtimeStart = time.Now().Unix()
	err = readyTemplate.Execute(outputFile, command.ImageConfig)
	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "generate text for Dockerfile", "success", err, unixtimeStart)
		result.Success = 0
		return result, nil
	}

	composeFile := "compose.dind.yaml"
	if command.ImageConfig.Image == "asdozzz/unitman-unit:latest" {
		composeFile = "compose.podman.yaml"
	}

	unixtimeStart = time.Now().Unix()
	err = CopyFileContents(filepath+"/"+composeFile, filepath+"/compose.yaml")
	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "make compose.yaml by image name", "success", err, unixtimeStart)
		return nil, err
	}

	// Тома готовятся до сборки образа: Dockerfile копирует ./app в /app,
	// поэтому распакованные архивы попадают в контейнер вместе с кодом.
	volumeSteps, errVolumes := PrepareVolumes(filepath, command.Volumes)
	result.Steps = append(result.Steps, volumeSteps...)
	if errVolumes != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	args := []string{"docker", "compose", "up", "-d", "--build"}
	unixtimeStart = time.Now().Unix()
	msg, err := utils.ExecCommand(filepath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	RestoreCache(command.ProjectName, command.Name, command.ProjectId, command.Id, command.Caches)
	//RestoreImages(command.ProjectId, command.Id)
	/*for i := 0; i < 15; i++ {
		args := []string{"docker", "compose", "exec", "unit", "sh", "-c", "docker info"}
		_, err = utils.ExecCommand(filepath, args)
		if err == nil {
			break
		} else {
			time.Sleep(500 * time.Millisecond)
		}
	}*/

	for _, commandString := range command.Commands {
		//commandArgs := strings.Split(commandString, " ")
		args := []string{"docker", "compose", "exec", "unit", "sh", "-c", commandString}
		unixtimeStart := time.Now().Unix()
		msg, errCommand := utils.ExecCommand(filepath, args)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand, unixtimeStart)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatPogotovki(result), nil
		} else {
			wrapPodgotovkaSaveStepsToMono(result)
		}
	}

	return wrapResultatPogotovkiSuccess(result), nil
}
