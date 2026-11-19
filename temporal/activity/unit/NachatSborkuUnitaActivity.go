package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	if strings.TrimSpace(command.Branch) == "" {
		result.Steps = model.AddStepToSteps(result.Steps, "check branch", "", fmt.Errorf("не задана ветка юнита"), time.Now().Unix())
		result.Success = 0
		return result, nil
	}

	filepathUnit := "./projects/" + command.ProjectId + "/units/" + command.Id
	filepathProject := "./projects/" + command.ProjectId + "/mainBranch"

	unixtimeStart = time.Now().Unix()
	err = os.MkdirAll(filepathUnit, os.ModePerm)
	result.Steps = model.AddStepToSteps(result.Steps, "MkdirAll unit dir", filepathUnit, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	currentPath, err := os.Getwd()
	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "", err, time.Now().Unix())
		result.Success = 0
		return result, nil
	}

	projectPath := filepath.Join(currentPath, filepathProject)
	branchRef := "refs/remotes/origin/" + command.Branch

	unixtimeStart = time.Now().Unix()
	args := []string{"git", "fetch", "--prune", "origin", "+refs/heads/" + command.Branch + ":" + branchRef}
	msg, err := utils.ExecCommand(projectPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	configCommand := "if git cat-file -e " + branchRef + ":unitman.yaml 2>/dev/null; then git show " + branchRef + ":unitman.yaml; fi"
	configOutput, err := utils.ExecCommand(projectPath, []string{"sh", "-c", configCommand})
	result.Steps = model.AddStepToSteps(result.Steps, configCommand, configOutput, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	result.Config = configOutput

	return result, nil
}
