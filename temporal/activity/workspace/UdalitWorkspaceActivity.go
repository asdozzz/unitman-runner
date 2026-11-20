package workspace

import (
	"context"
	"os"
	"strings"
	"time"

	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
)

// UdalitWorkspaceActivity удаляет контейнеры, тома и каталог workspace.
// `docker compose down -v --remove-orphans` не удаляет external-тома, поэтому
// они удаляются явно. Операция идемпотентна.
func UdalitWorkspaceActivity(
	ctx context.Context,
	command WorkspaceRef,
) (*ResultatOperaciiWorkspace, error) {
	responseID := workflowID(ctx)
	result := &ResultatOperaciiWorkspace{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: responseID,
	}
	tracker := newStepTracker(responseID)

	if err := validateWorkspaceID(command.WorkspaceId); err != nil {
		result.Success = 0
		tracker.add("validate workspace", "", err, time.Now())
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	workspaceDir := workspaceDirectory(command.WorkspaceId)

	if info, statErr := os.Stat(workspaceDir); statErr == nil && info.IsDir() {
		started := time.Now()
		args := []string{"docker", "compose", "down", "-v", "--remove-orphans"}
		msg, err := utils.ExecCommandContext(ctx, workspaceDir, args)
		tracker.add(strings.Join(args, " "), strings.TrimSpace(msg), err, started)
		if err != nil {
			result.Success = 0
			tracker.publish(true)
			result.Steps = []model.Step{}
			return result, nil
		}

		tracker.publish(false)
	}

	volumes := WorkspaceVolumesFor(command.WorkspaceId)
	started := time.Now()
	args := append([]string{"docker", "volume", "rm", "-f"}, volumes.Names()...)
	msg, err := utils.ExecCommandContext(ctx, workRoot(), args)
	if err != nil && isMissingVolumeError(err) {
		err = nil
	}
	tracker.add(strings.Join(args, " "), strings.TrimSpace(msg), err, started)
	if err != nil {
		result.Success = 0
		tracker.publish(true)
		result.Steps = []model.Step{}
		return result, nil
	}

	started = time.Now()
	removeErr := os.RemoveAll(workspaceDir)
	tracker.add("remove workspace directory", "", removeErr, started)
	if removeErr != nil {
		result.Success = 0
	}

	tracker.publish(true)
	result.Steps = []model.Step{}
	return result, nil
}

// isMissingVolumeError распознаёт отсутствие тома: удаление должно быть
// идемпотентным.
func isMissingVolumeError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such volume") || strings.Contains(message, "not found")
}
