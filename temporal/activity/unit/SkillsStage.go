package unit

import (
	"context"
	"fmt"
	"path/filepath"

	"runner/temporal/activity/skills"
)

// UnitSkillsStagingDir возвращает staging-каталог навыков юнита
// (<unit>/seed/skills), который монтируется в контейнер read-only.
func UnitSkillsStagingDir(unitPath string) string {
	return filepath.Join(unitPath, unitStagingDirName, unitStagingSkillsDirName)
}

// StageUnitSkills загружает навыки для одного проекта и раскладывает их в
// staging-каталог юнита. Ошибка не фатальна: вызывающий код логирует её и
// продолжает подготовку.
func StageUnitSkills(ctx context.Context, unitPath, projectID string) (skills.Outcome, error) {
	return skills.FetchAndStage(ctx, UnitSkillsStagingDir(unitPath), []string{projectID})
}

// SkillsStepMessage формирует читаемое сообщение шага прогресса. Ошибка
// навыков намеренно сообщается как «non-fatal», чтобы шаг не помечал
// подготовку проваленной.
func SkillsStepMessage(outcome skills.Outcome, err error) string {
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
