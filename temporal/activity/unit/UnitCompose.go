package unit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
)

const (
	unitTemplatesRelativePath = "temporal/activity/unit/docker-dind"
	unitDindComposeFile       = "compose.dind.yaml"
	unitPodmanComposeFile     = "compose.podman.yaml"
	unitPodmanImageName       = "asdozzz/unitman-unit:latest"
)

// PrepareUnitFiles копирует служебные файлы юнита, выбирает compose-файл
// и генерирует Dockerfile из шаблона.
func PrepareUnitFiles(unitPath string, imageConfig model.ImageConfigStruct) ([]model.Step, error) {
	steps := []model.Step{}

	currentPath, err := os.Getwd()
	if err != nil {
		steps = model.AddStepToSteps(steps, "Getwd", "", err, time.Now().Unix())
		return steps, err
	}

	templatesPath := filepath.Join(currentPath, unitTemplatesRelativePath)

	unixtimeStart := time.Now().Unix()
	args := []string{"cp", "-R", templatesPath + "/.", unitPath}
	msg, err := utils.ExecCommand(unitPath, args)
	steps = model.AddStepToSteps(steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		return steps, fmt.Errorf("не удалось скопировать служебные файлы юнита: %w", err)
	}

	composeFile := SelectUnitComposeFile(imageConfig.Image)

	unixtimeStart = time.Now().Unix()
	err = CopyFileContents(filepath.Join(unitPath, composeFile), filepath.Join(unitPath, "compose.yaml"))
	steps = model.AddStepToSteps(steps, "make compose.yaml from "+composeFile, "", err, unixtimeStart)
	if err != nil {
		return steps, fmt.Errorf("не удалось подготовить compose.yaml: %w", err)
	}

	unixtimeStart = time.Now().Unix()
	readyTemplate, err := template.ParseFiles(filepath.Join(unitPath, "Dockerfile.temp"))
	steps = model.AddStepToSteps(steps, "parse Dockerfile template", "", err, unixtimeStart)
	if err != nil {
		return steps, fmt.Errorf("не удалось разобрать Dockerfile.temp: %w", err)
	}

	unixtimeStart = time.Now().Unix()
	outputFile, err := os.Create(filepath.Join(unitPath, "Dockerfile"))
	steps = model.AddStepToSteps(steps, "make Dockerfile", "", err, unixtimeStart)
	if err != nil {
		return steps, fmt.Errorf("не удалось создать Dockerfile: %w", err)
	}
	defer outputFile.Close()

	unixtimeStart = time.Now().Unix()
	err = readyTemplate.Execute(outputFile, imageConfig)
	steps = model.AddStepToSteps(steps, "generate text for Dockerfile", "", err, unixtimeStart)
	if err != nil {
		return steps, fmt.Errorf("не удалось сгенерировать Dockerfile: %w", err)
	}

	return steps, nil
}

// SelectUnitComposeFile выбирает compose-файл по образу юнита.
func SelectUnitComposeFile(image string) string {
	if image == unitPodmanImageName {
		return unitPodmanComposeFile
	}

	return unitDindComposeFile
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
func EnsureUnitSeedDir(unitPath string) error {
	return os.MkdirAll(filepath.Join(unitPath, unitStagingDirName, unitStagingVolumesDirName), os.ModePerm)
}

// RemoveLegacyHostApp удаляет старый host-каталог app после успешного seed.
func RemoveLegacyHostApp(unitPath string) error {
	return os.RemoveAll(filepath.Join(unitPath, "app"))
}
