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
	"runner/threat"
	"strings"
	"time"
)

type NachatObnovlenieUnita struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	// StorageUrl — чистый remote URL без токена. Токен запрашивается
	// через broker по RepoId и живёт только на время fetch.
	StorageUrl string
	RepoId     string
}

type ResultatObnovleniyaUnita struct {
	Success int
	Steps   []model.Step
	Config  string
}

func NachatObnovlenieUnitaActivity(ctx context.Context, command NachatObnovlenieUnita) (result *ResultatObnovleniyaUnita, err error) {
	result = &ResultatObnovleniyaUnita{
		Success: 1,
		Steps:   []model.Step{},
	}

	defer func() {
		success := false
		if result != nil {
			success = result.Success == 1
		}
		state.RecordActivityResult("obnovlenie", command.ProjectId, command.Id, command.Name, command.ProjectName, success)
	}()

	// Ресурсоёмкая активность: при уровне блокировки не перезапускаем юнит.
	if blocked, message := threat.BlockedMessage(); blocked {
		result.Steps = model.AddStepToSteps(result.Steps, resursnayaProverkaStep, message, fmt.Errorf("%s", message), time.Now().Unix())
		result.Success = 0
		return result, nil
	}

	unixtimeStart := time.Now().Unix()
	_, err = json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	utils.LogActivityCommand("NachatObnovlenieUnita", command)

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
	grant, grantErr := fetchRepoGrant(ctx, command.RepoId, command.ProjectId, command.Id)
	result.Steps = model.AddStepToSteps(result.Steps, "fetch repo credentials", "", grantErr, unixtimeStart)
	if grantErr != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = time.Now().Unix()
	msg, err = SyncUnitRepo(ctx, unitPath, branch, command.StorageUrl, grant)
	result.Steps = model.AddStepToSteps(result.Steps, "unit-repo-sync branch="+branchLabel, msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	// Обновление кода всегда оставляет безопасное git-состояние: без токена в
	// origin и без push-креденшелов. Сессия редактора переподготовится отдельно.
	unixtimeStart = time.Now().Unix()
	err = PrepareUnitGitSecurity(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "prepare git security", "", err, unixtimeStart)
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
