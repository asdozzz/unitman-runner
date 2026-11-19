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

type NachatObnovlenieUnita struct {
	ProjectId string
	Id        string
	Name      string
}

type ResultatObnovleniyaUnita struct {
	Success int
	Steps   []model.Step
	Config  string
}

func NachatObnovlenieUnitaActivity(ctx context.Context, command NachatObnovlenieUnita) (*ResultatObnovleniyaUnita, error) {
	result := &ResultatObnovleniyaUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	unixtimeStart := time.Now().Unix()
	out, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	fmt.Println("NachatObnovlenieUnita:" + string(out))

	filepathUnit := "./projects/" + command.ProjectId + "/units/" + command.Id

	unixtimeStart = time.Now().Unix()
	currentPath, err := os.Getwd()
	result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unitPath := filepath.Join(currentPath, filepathUnit)

	for _, requiredFile := range []string{"compose.yaml", ".env"} {
		unixtimeStart = time.Now().Unix()
		_, err = os.Stat(filepath.Join(unitPath, requiredFile))
		result.Steps = model.AddStepToSteps(result.Steps, "check "+requiredFile, "", err, unixtimeStart)
		if err != nil {
			result.Success = 0
			return result, nil
		}
	}

	volumeSteps, err := EnsureUnitVolumes(unitPath, command.Id)
	result.Steps = append(result.Steps, volumeSteps...)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	args := []string{"docker", "compose", "up", "-d"}
	unixtimeStart = time.Now().Unix()
	msg, err := utils.ExecCommand(unitPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	err = WaitUnitReady(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "wait unit ready", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	branch := ReadUnitEnvValue(unitPath, "UNITMAN_BRANCH_NAME")
	branchLabel := branch
	if branchLabel == "" {
		branchLabel = "current"
	}

	unixtimeStart = time.Now().Unix()
	msg, err = SyncUnitRepo(unitPath, branch, "")
	result.Steps = model.AddStepToSteps(result.Steps, "unit-repo-sync branch="+branchLabel, msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	unitConfig, err := ReadUnitConfig(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "read unitman.yaml", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	result.Config = unitConfig

	return result, nil
}
