package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"runner/threat"
)

// urlUserInfoRe маскирует учётные данные в URL внутри текста вывода.
var urlUserInfoRe = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+@`)

const (
	errorKeyBranchNotFound = "opencode.workspace.project_branch_not_found"
	errorKeyCloneFailed    = "opencode.workspace.project_clone_failed"
	errorKeyOperation      = "opencode.workspace.operation_failed"
)

// ZapustitWorkspaceActivity создаёт/обновляет workspace: раскладку, тома,
// контейнеры workspace+IDE, клоны проектов и multi-root workspace-файл.
// Активность идемпотентна: существующие каталоги и проекты переиспользуются.
func ZapustitWorkspaceActivity(
	ctx context.Context,
	command ZapustitWorkspaceCommand,
) (*ResultatZapuskaWorkspace, error) {
	responseID := workflowID(ctx)
	result := &ResultatZapuskaWorkspace{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: responseID,
		Projects:   []ProjectResult{},
	}
	tracker := newStepTracker(responseID)

	defer func() {
		// Паника не должна утекать в Temporal, но и молча терять шаги нельзя.
		if r := recover(); r != nil {
			result.Success = 0
			started := time.Now()
			tracker.add("zapustit workspace", fmt.Sprint(r), errors.New(fmt.Sprint(r)), started)
			tracker.publish(true)
			result.Steps = []model.Step{}
		}
	}()

	// Запуск workspace поднимает контейнеры и клонирует проекты — ресурсоёмкая
	// операция. Остановка и удаление остаются разрешёнными, чтобы можно было
	// освободить ресурсы.
	if blocked, message := threat.BlockedMessage(); blocked {
		result.Success = 0
		tracker.add(resursnayaProverkaStep, message, errors.New(message), time.Now())
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	if err := validateWorkspaceID(command.WorkspaceId); err != nil {
		result.Success = 0
		tracker.add("validate workspace", "", err, time.Now())
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	if len(command.Projects) == 0 {
		result.Success = 0
		tracker.add("validate projects", "", errors.New("список проектов пуст"), time.Now())
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	workspaceDir := workspaceDirectory(command.WorkspaceId)

	started := time.Now()
	err := os.MkdirAll(workspaceDir, os.ModePerm)
	tracker.add("mkdir "+workspaceDir, "", err, started)
	if err != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	if err := prepareWorkspaceFiles(workspaceDir, command, tracker); err != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	if volErr := ensureVolumes(workspaceDir, command.WorkspaceId, tracker); volErr != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	tracker.publish(false)

	started = time.Now()
	upArgs := []string{"docker", "compose", "up", "-d", "--build"}
	msg, upErr := utils.ExecCommandContext(ctx, workspaceDir, upArgs)
	tracker.add(strings.Join(upArgs, " "), strings.TrimSpace(msg), upErr, started)
	if upErr != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	tracker.publish(false)

	started = time.Now()
	readyErr := waitWorkspaceReady(ctx, workspaceDir)
	tracker.add("wait workspace ready", "", readyErr, started)
	if readyErr != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	started = time.Now()
	ideErr := waitIDEHealthy(ctx, workspaceDir, command.WorkspaceSlug)
	tracker.add("wait ide healthy", "", ideErr, started)
	if ideErr != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	tracker.publish(false)

	allProjectsOK := true
	for _, project := range command.Projects {
		started = time.Now()
		output, syncErr := syncProject(ctx, workspaceDir, command, project)
		tracker.add("sync project "+project.ProjectName, redactURL(output), syncErr, started)

		projectResult := ProjectResult{
			ProjectId: project.ProjectId,
			Name:      project.ProjectName,
			Success:   syncErr == nil,
		}

		if syncErr != nil {
			allProjectsOK = false
			key := errorKeyFromOutput(output)
			if key == "" {
				key = errorKeyOperation
			}
			projectResult.ErrorKey = &key
		}

		result.Projects = append(result.Projects, projectResult)
		tracker.publish(false)
	}

	if genErr := generateCodeWorkspace(ctx, workspaceDir, command.Projects, tracker); genErr != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	if allProjectsOK {
		result.Success = 1
	} else {
		result.Success = 0
	}

	tracker.publish(true)
	result.Steps = []model.Step{}
	return result, nil
}

// redactURL убирает учётные данные из текста вывода: токен не должен попасть
// в шаги прогресса.
func redactURL(value string) string {
	return urlUserInfoRe.ReplaceAllString(value, "${1}redacted@")
}
