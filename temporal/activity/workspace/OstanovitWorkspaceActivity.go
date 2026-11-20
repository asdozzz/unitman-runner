package workspace

import (
	"context"
	"os"
	"strings"
	"time"

	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
)

// OstanovitWorkspaceActivity останавливает контейнеры workspace и IDE.
// Выполняется только `docker compose stop`; тома и каталог сохраняются.
func OstanovitWorkspaceActivity(
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

	info, statErr := os.Stat(workspaceDir)
	if statErr != nil || !info.IsDir() {
		// Останавливать нечего: операция идемпотентна.
		result.Success = 1
		return result, nil
	}

	started := time.Now()
	args := []string{"docker", "compose", "stop"}
	msg, err := utils.ExecCommandContext(ctx, workspaceDir, args)
	tracker.add(strings.Join(args, " "), strings.TrimSpace(msg), err, started)
	if err != nil {
		result.Success = 0
	}

	tracker.publish(true)
	result.Steps = []model.Step{}
	return result, nil
}
