package unit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"runner/temporal/activity/unit/model"
)

const (
	unitTemplatesRelativePath = "temporal/activity/unit/docker-dind"
	unitComposeTemplate       = "compose.dind.yaml"
	unitComposeTarget         = "compose.yaml"
	unitIdeViewTemplate       = "compose.ide-view.yaml"
	unitIdeEditTemplate       = "compose.ide-edit.yaml"
)

// PrepareUnitFiles копирует статичные compose-шаблоны в рабочий каталог юнита:
// базовый compose.yaml и два неизменяемых override-файла для режимов IDE.
// Dockerfile не генерируется: образы unit и ide предсобраны и подключаются
// через image:, а не build:.
func PrepareUnitFiles(unitPath string) ([]model.Step, error) {
	steps := []model.Step{}

	currentPath, err := os.Getwd()
	if err != nil {
		steps = model.AddStepToSteps(steps, "Getwd", "", err, time.Now().Unix())
		return steps, err
	}

	templates := []struct {
		source string
		target string
	}{
		{unitComposeTemplate, unitComposeTarget},
		{unitIdeViewTemplate, unitIdeViewTemplate},
		{unitIdeEditTemplate, unitIdeEditTemplate},
	}

	for _, template := range templates {
		source := filepath.Join(currentPath, unitTemplatesRelativePath, template.source)
		target := filepath.Join(unitPath, template.target)

		unixtimeStart := time.Now().Unix()
		err = CopyFileContents(source, target)
		steps = model.AddStepToSteps(steps, "make "+template.target+" from "+template.source, "", err, unixtimeStart)
		if err != nil {
			return steps, fmt.Errorf("не удалось подготовить %s: %w", template.target, err)
		}
	}

	return steps, nil
}

// WriteUnitEnvFile атомарно перезаписывает .env юнита.
// Порядок записей задаёт вызывающий код, чтобы файл был детерминированным.
func WriteUnitEnvFile(unitPath string, entries [][2]string) error {
	builder := strings.Builder{}

	for _, entry := range entries {
		key := strings.TrimSpace(entry[0])
		if key == "" || strings.ContainsAny(key, "=\r\n") {
			return fmt.Errorf("некорректное имя переменной окружения: %q", entry[0])
		}

		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(entry[1])
		builder.WriteString("\n")
	}

	envPath := filepath.Join(unitPath, ".env")

	return os.WriteFile(envPath, []byte(builder.String()), 0644)
}

// EnsureUnitSeedDir создаёт staging-каталоги до compose up, чтобы Docker
// не создал их с правами root и не сломал запись из runner.
//
// seed/skills создаётся заранее, потому что оно монтируется в контейнер
// read-only (./seed/skills:/opt/unitman/skills:ro): если каталога нет,
// Docker создаст его от root и staging навыков из runner станет невозможен.
func EnsureUnitSeedDir(unitPath string) error {
	seedPath := filepath.Join(unitPath, unitStagingDirName)
	for _, name := range []string{unitStagingVolumesDirName, unitStagingSkillsDirName} {
		if err := os.MkdirAll(filepath.Join(seedPath, name), os.ModePerm); err != nil {
			return err
		}
	}

	return nil
}

// RemoveLegacyHostApp удаляет старый host-каталог app после успешного seed.
func RemoveLegacyHostApp(unitPath string) error {
	return os.RemoveAll(filepath.Join(unitPath, "app"))
}
