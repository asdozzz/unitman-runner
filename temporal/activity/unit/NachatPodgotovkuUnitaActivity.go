package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runner/config"
	"runner/state"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"runner/threat"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

type ContainerSettings struct {
	MemoryLimit string
}

type NachatPodgotovkuUnita struct {
	ProjectId         string
	ProjectName       string
	Id                string
	Name              string
	StorageUrl        string
	Commands          []string
	Variables         []model.UnitConfigVariable
	ContainerSettings ContainerSettings
	Branch            string
	Volumes           []Volume
	// RezhimRedaktora — режим IDE-сайдкара `view`|`edit`. Отсутствие поля у
	// старых бэкендов трактуется как `view` (обратная совместимость).
	RezhimRedaktora string
}

type ResultatPodgotovkiUnita struct {
	Success    int
	Steps      []model.Step
	Config     string
	ResponseId string
}

func wrapPodgotovkaSaveStepsToMono(result *ResultatPodgotovkiUnita) {
	SaveStepsToMono(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
}

func wrapResultatPogotovki(result *ResultatPodgotovkiUnita) *ResultatPodgotovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: false})
	return result
}

func wrapResultatPogotovkiSuccess(result *ResultatPodgotovkiUnita) *ResultatPodgotovkiUnita {
	SaveUnitSteps(SaveStepsCommand{ResponseId: result.ResponseId, Steps: &result.Steps, Finished: true})
	return result
}

func NachatPodgotovkuUnitaActivity(ctx context.Context, command NachatPodgotovkuUnita) (result *ResultatPodgotovkiUnita, err error) {
	result = &ResultatPodgotovkiUnita{
		Success:    1,
		Steps:      []model.Step{},
		ResponseId: activity.GetInfo(ctx).WorkflowExecution.ID,
	}

	defer func() {
		success := false
		if result != nil {
			success = result.Success == 1
		}
		state.RecordActivityResult("podgotovka", command.ProjectId, command.Id, command.Name, command.ProjectName, success)
	}()

	// Ресурсоёмкая активность: при уровне блокировки не поднимаем контейнеры.
	if blocked, message := threat.BlockedMessage(); blocked {
		result.Steps = model.AddStepToSteps(result.Steps, resursnayaProverkaStep, message, fmt.Errorf("%s", message), time.Now().Unix())
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	if _, marshalErr := json.Marshal(command); marshalErr != nil {
		fmt.Println(marshalErr)
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	utils.LogActivityCommand("ResultatPodgotovkiUnita", command)

	filepathUnit := "./projects/" + command.ProjectId + "/units/" + command.Id

	currentPath, err := os.Getwd()
	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "", err, time.Now().Unix())
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	unitPath := filepath.Join(currentPath, filepathUnit)

	unixtimeStart := time.Now().Unix()
	err = os.MkdirAll(unitPath, os.ModePerm)
	result.Steps = model.AddStepToSteps(result.Steps, "MkdirAll unit dir", unitPath, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	fileSteps, err := PrepareUnitFiles(unitPath)
	result.Steps = append(result.Steps, fileSteps...)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	// Режим IDE сохраняется отдельным файлом и выбирает статичный
	// compose.ide-view.yaml или compose.ide-edit.yaml при запуске IDE.
	rezhim := NormalizeRezhimRedaktora(command.RezhimRedaktora)
	unixtimeStart = time.Now().Unix()
	err = WriteIdeMode(unitPath, rezhim)
	result.Steps = model.AddStepToSteps(result.Steps, "apply ide mode="+rezhim, "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	unixtimeStart = time.Now().Unix()
	err = EnsureUnitSeedDir(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "mkdir unit seed dir", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	volumes := UnitVolumes(command.Id)

	// Реестр образов выдаётся backend-ом при регистрации и хранится в
	// config.json. Fallback на прежние имена реестра запрещён.
	configuration, err := config.ParseConfig()
	result.Steps = model.AddStepToSteps(result.Steps, "read config.json", "", err, time.Now().Unix())
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	registryHost, cacheRepo, err := ResolveRegistry(configuration.RegistryHost)
	result.Steps = model.AddStepToSteps(result.Steps, "resolve registry host", "", err, time.Now().Unix())
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	// Образы юнита и IDE фиксируются версией релиза (ldflags) и реестром
	// установки; конфиг проекта на них не влияет.
	unitImage := configuration.UnitImageRef()
	ideImage := configuration.IdeImageRef()

	envEntries := [][2]string{
		{"UNITMAN_UNIT_ID", command.Id},
		{"UNITMAN_UNIT_NAME", command.Name},
		{"UNITMAN_PROJECT_NAME", command.ProjectName},
		{"UNITMAN_BRANCH_NAME", command.Branch},
		{"UNITMAN_UNIT_IMAGE", unitImage},
		{"UNITMAN_IDE_IMAGE", ideImage},
		{"COMPOSE_PROJECT_NAME", command.Name + "_" + command.ProjectName},
		{"UNITMAN_UNIT_APP_VOLUME", volumes.App},
		{"UNITMAN_UNIT_DOCKER_VOLUME", volumes.Docker},
		{"UNITMAN_MEMORY_LIMIT", command.ContainerSettings.MemoryLimit},
		{"UNITMAN_REGISTRY_HOST", registryHost},
		{"UNITMAN_CACHE_REPO", cacheRepo},
	}

	for _, variableItem := range command.Variables {
		envEntries = append(envEntries, [2]string{"UNITMAN_" + variableItem.Id, variableItem.Value})
	}

	unixtimeStart = time.Now().Unix()
	err = WriteUnitEnvFile(unitPath, envEntries)
	result.Steps = model.AddStepToSteps(result.Steps, "write .env", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	volumeSteps, err := EnsureUnitVolumes(unitPath, command.Id)
	result.Steps = append(result.Steps, volumeSteps...)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	// IDE вынесена в compose-профиль и стартует по требованию при открытии
	// редактора, поэтому подготовка поднимает только контейнер unit.
	args := []string{"docker", "compose", "up", "-d"}
	unixtimeStart = time.Now().Unix()
	msg, err := utils.ExecCommand(unitPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	unixtimeStart = time.Now().Unix()
	err = WaitUnitReady(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "wait unit ready", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	unixtimeStart = time.Now().Unix()
	msg, err = SyncUnitRepo(unitPath, command.Branch, command.StorageUrl)
	result.Steps = model.AddStepToSteps(result.Steps, "unit-repo-sync branch="+command.Branch, msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	// После клонирования/обновления токен админа больше не хранится в origin,
	// а push без активной редакторской сессии запрещён pre-push хуком.
	unixtimeStart = time.Now().Unix()
	err = PrepareUnitGitSecurity(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "prepare git security", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	volumeSteps, stagedVolumes, err := PrepareVolumes(unitPath, command.Volumes, func(dir string) (bool, error) {
		return UnitVolumeSeeded(unitPath, dir)
	})
	result.Steps = append(result.Steps, volumeSteps...)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	if stagedVolumes > 0 {
		unixtimeStart = time.Now().Unix()
		msg, err = SyncUnitVolumes(unitPath)
		result.Steps = model.AddStepToSteps(result.Steps, "unit-volumes-sync", msg, err, unixtimeStart)
		if err != nil {
			result.Success = 0
			return wrapResultatPogotovki(result), nil
		}
	}

	unixtimeStart = time.Now().Unix()
	err = RemoveLegacyHostApp(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "remove legacy host app", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	for _, commandString := range command.Commands {
		args := UnitContainerCommand(commandString)
		unixtimeStart := time.Now().Unix()
		msg, errCommand := ExecInUnitContainer(unitPath, commandString)
		result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, errCommand, unixtimeStart)
		if errCommand != nil {
			result.Success = 0
			return wrapResultatPogotovki(result), nil
		}

		wrapPodgotovkaSaveStepsToMono(result)
	}

	unixtimeStart = time.Now().Unix()
	unitConfig, err := ReadUnitConfig(unitPath)
	result.Steps = model.AddStepToSteps(result.Steps, "read unitman.yaml", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return wrapResultatPogotovki(result), nil
	}

	result.Config = unitConfig

	return wrapResultatPogotovkiSuccess(result), nil
}

// ResolveRegistry нормализует registryHost из config.json к каноническому виду
// scheme://host[:port] без завершающего слэша и возвращает authority host[:port]
// для docker-ссылок (UNITMAN_CACHE_REPO). Экспортирован, чтобы path workspace
// использовал ту же нормализацию и не дублировал логику.
func ResolveRegistry(value string) (string, string, error) {
	registryHost := strings.TrimRight(strings.TrimSpace(value), "/")
	if registryHost == "" {
		return "", "", fmt.Errorf("registryHost не задан в config.json: выполните runner init")
	}
	if !strings.Contains(registryHost, "://") {
		registryHost = "http://" + registryHost
	}

	parsed, err := url.Parse(registryHost)
	if err != nil || parsed.Host == "" {
		return "", "", fmt.Errorf("некорректный registryHost %q", value)
	}

	return parsed.Scheme + "://" + parsed.Host, parsed.Host, nil
}
