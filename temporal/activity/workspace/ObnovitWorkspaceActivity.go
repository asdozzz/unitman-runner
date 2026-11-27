package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"runner/temporal/activity/unit/model"
)

// ObnovitWorkspaceCommand — вход ObnovitWorkspaceActivity.
type ObnovitWorkspaceCommand struct {
	WorkspaceId   string
	WorkspaceSlug string
	Branch        string
	ReadOnly      bool
	NewProjects   []Project
	AllProjects   []Project
	Files         []WorkspaceFile
	FilesToDelete []string
}

// ResultatObnovleniyaWorkspace — результат обновления workspace.
type ResultatObnovleniyaWorkspace struct {
	Success    int
	Steps      []model.Step
	ResponseId string
	Projects   []ProjectResult
	Files      []FileResult
}

// FileResult — результат применения одного файла.
type FileResult struct {
	FileId   string
	Name     string
	Success  bool
	ErrorKey *string
}

// ObnovitWorkspaceActivity добавляет проекты и файлы в работающий workspace,
// не пересоздавая контейнер и не трогая существующие проекты.
func ObnovitWorkspaceActivity(
	ctx context.Context,
	command ObnovitWorkspaceCommand,
) (*ResultatObnovleniyaWorkspace, error) {
	responseID := workflowID(ctx)
	result := &ResultatObnovleniyaWorkspace{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: responseID,
		Projects:   []ProjectResult{},
		Files:      []FileResult{},
	}
	tracker := newStepTracker(responseID)

	defer func() {
		if r := recover(); r != nil {
			result.Success = 0
			tracker.add("obnovit workspace", fmt.Sprint(r), errors.New(fmt.Sprint(r)), time.Now())
			tracker.publish(true)
			result.Steps = []model.Step{}
		}
	}()

	if err := validateWorkspaceID(command.WorkspaceId); err != nil {
		result.Success = 0
		tracker.add("validate workspace", "", err, time.Now())
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	workspaceDir := workspaceDirectory(command.WorkspaceId)

	started := time.Now()
	_, err := os.Stat(workspaceDir)
	tracker.add("check workspace directory", workspaceDir, err, started)
	if err != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	if readyErr := waitWorkspaceReady(ctx, workspaceDir); readyErr != nil {
		result.Success = 0
		tracker.add("wait workspace ready", "", readyErr, time.Now())
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	syncCommand := ZapustitWorkspaceCommand{
		WorkspaceId: command.WorkspaceId,
		Branch:      command.Branch,
		ReadOnly:    command.ReadOnly,
	}

	allOK := true

	for _, project := range command.NewProjects {
		started = time.Now()
		grant, grantErr := fetchWorkspaceRepoGrant(ctx, project.RepoId, project.ProjectId, "")
		if grantErr != nil {
			tracker.add("fetch repo credentials "+project.ProjectName, "", grantErr, started)
			key := errorKeyCredentials
			result.Projects = append(result.Projects, ProjectResult{
				ProjectId: project.ProjectId,
				Name:      project.ProjectName,
				Success:   false,
				ErrorKey:  &key,
			})
			allOK = false
			continue
		}

		started = time.Now()
		output, syncErr := syncProject(ctx, workspaceDir, syncCommand, project, grant)
		tracker.add("sync project "+project.ProjectName, redactURL(output), syncErr, started)

		projectResult := ProjectResult{
			ProjectId: project.ProjectId,
			Name:      project.ProjectName,
			Success:   syncErr == nil,
		}

		if syncErr != nil {
			allOK = false
			key := errorKeyFromOutput(output)
			if key == "" {
				key = errorKeyOperation
			}
			projectResult.ErrorKey = &key
		}

		result.Projects = append(result.Projects, projectResult)
		tracker.publish(false)
	}

	if genErr := generateCodeWorkspace(ctx, workspaceDir, command.AllProjects, true, tracker); genErr != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	if applyErr := applyWorkspaceFiles(ctx, workspaceDir, command.Files, tracker); applyErr != nil {
		allOK = false
	}

	if removeErr := removeWorkspaceFiles(ctx, workspaceDir, command.FilesToDelete, tracker); removeErr != nil {
		allOK = false
	}

	for _, file := range command.Files {
		result.Files = append(result.Files, FileResult{
			FileId:  file.FileId,
			Name:    file.Name,
			Success: allOK,
		})
	}

	if allOK {
		result.Success = 1
	} else {
		result.Success = 0
	}

	tracker.publish(true)
	result.Steps = []model.Step{}
	return result, nil
}
