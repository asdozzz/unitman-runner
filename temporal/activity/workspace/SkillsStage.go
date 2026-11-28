package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"runner/temporal/activity/skills"
	"runner/temporal/utils"
)

// workspaceSkillsDirName — staging-каталог навыков внутри раскладки workspace.
const workspaceSkillsDirName = "skills"

// WorkspaceSkillsStagingDir возвращает <workspace>/seed/skills.
func WorkspaceSkillsStagingDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, "seed", workspaceSkillsDirName)
}

// stageWorkspaceSkills резолвит навыки для всех проектов workspace и
// раскладывает их в staging. Ошибка не фатальна для запуска workspace.
func stageWorkspaceSkills(ctx context.Context, workspaceDir string, projects []Project) (skills.Outcome, error) {
	projectIDs := make([]string, 0, len(projects))
	for _, project := range projects {
		if id := strings.TrimSpace(project.ProjectId); id != "" {
			projectIDs = append(projectIDs, id)
		}
	}

	return skills.FetchAndStage(ctx, WorkspaceSkillsStagingDir(workspaceDir), projectIDs)
}

// workspaceSkillsStepMessage формирует сообщение шага. Ошибка сообщается как
// «non-fatal», чтобы шаг не помечал запуск проваленным.
func workspaceSkillsStepMessage(outcome skills.Outcome, err error) string {
	if err != nil {
		return "non-fatal: " + err.Error()
	}

	message := fmt.Sprintf("staged %d skill(s), %d bytes", outcome.Count, outcome.TotalBytes)
	if outcome.Skipped > 0 {
		message += fmt.Sprintf(", skipped %d", outcome.Skipped)
	}
	if outcome.NotDeployed {
		message += " (feature not deployed)"
	}
	if outcome.Truncated {
		message += " (truncated)"
	}

	return message
}

// runWorkspaceSkillsSync запускает in-container unit-skills-sync. Best-effort:
// ошибка не влияет на результат активности. Если контейнер ещё не поднят,
// навыки материализуются на следующем ходе агента.
func runWorkspaceSkillsSync(ctx context.Context, workspaceDir string) (string, error) {
	return utils.ExecCommandContext(ctx, workspaceDir, workspaceCommand(
		"unit-skills-sync || true", "", nil,
	))
}
