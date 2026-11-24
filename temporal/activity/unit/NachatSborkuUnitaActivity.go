package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runner/config"
	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
	"runner/threat"
	"strings"
	"time"
)

// runnerBackendURLEnv — рантайм-override адреса backend-а, который уже
// используется в temporal/main.go. Если он пуст, берётся config.ApiHost.
const runnerBackendURLEnv = "RUNNER_BACKEND_URL"

type NachatSborkuUnita struct {
	ProjectId  string
	Id         string
	Name       string
	Branch     string
	StorageUrl string
	// RunnerId/TaskQueue добавляет backend в multi-runner режиме. Старый
	// backend их не присылает — тогда runnerId берётся из config.json.
	RunnerId  string
	TaskQueue string
}

type ResultatSbrokiUnita struct {
	Success int
	Steps   []model.Step
	Config  string
}

// sborkaDependencies инъектирует внешние эффекты активности, чтобы тесты
// проверяли ленивую сборку без реального git/docker/backend.
type sborkaDependencies struct {
	blocked             func() (bool, string)
	parseConfig         func() (config.Configuration, error)
	getenv              func(string) string
	getwd               func() (string, error)
	mkdirAll            func(string, os.FileMode) error
	execCommand         func(string, []string) (string, error)
	now                 func() time.Time
	httpClient          func() *http.Client
	ensureProjectRepo   func(ctx context.Context, projectDir, storageURL string) (string, string, error)
	newBuildStateClient func(baseURL, runnerID, secret string, httpClient *http.Client) *ProjectBuildStateClient
}

// defaultSborkaDependencies — боевые реализации.
func defaultSborkaDependencies() sborkaDependencies {
	return sborkaDependencies{
		blocked:           threat.BlockedMessage,
		parseConfig:       config.ParseConfig,
		getenv:            os.Getenv,
		getwd:             os.Getwd,
		mkdirAll:          os.MkdirAll,
		execCommand:       utils.ExecCommand,
		now:               time.Now,
		httpClient:        func() *http.Client { return &http.Client{Timeout: ProjectBuildStateDefaultTimeout} },
		ensureProjectRepo: ensureProjectMainBranch,
		newBuildStateClient: func(baseURL, runnerID, secret string, httpClient *http.Client) *ProjectBuildStateClient {
			return NewProjectBuildStateClient(baseURL, runnerID, secret, httpClient)
		},
	}
}

func NachatSborkuUnitaActivity(ctx context.Context, command NachatSborkuUnita) (*ResultatSbrokiUnita, error) {
	return nachatSborkuUnitaActivity(ctx, command, defaultSborkaDependencies())
}

// nachatSborkuUnitaActivity — ядро активности.
//
// Ленивая сборка проекта (plans/runner-multi-runner.md §3/§7):
//  1. перед fetch ветки юнита проверяем project_build_state для (runnerId, projectId);
//  2. если built=false — клонируем проект в projects/<ProjectId>/mainBranch и
//     записываем факт сборки идемпотентным PUT;
//  3. если built=true — clone пропускается, сразу идёт fetch ветки юнита.
//
// Fallback «считаем собранным по умолчанию» запрещён: ошибка backend-а
// завершает шаг активности, чтобы не собирать поверх неизвестного состояния.
// Повторный запуск безопасен: GET перечитывается, PUT — upsert.
func nachatSborkuUnitaActivity(ctx context.Context, command NachatSborkuUnita, deps sborkaDependencies) (*ResultatSbrokiUnita, error) {
	result := &ResultatSbrokiUnita{
		Success: 1,
		Steps:   []model.Step{},
		Config:  "",
	}

	// Ресурсоёмкая активность: при уровне блокировки не запускаем сборку.
	if blocked, message := deps.blocked(); blocked {
		result.Steps = model.AddStepToSteps(result.Steps, resursnayaProverkaStep, message, fmt.Errorf("%s", message), deps.now().Unix())
		result.Success = 0
		return result, nil
	}

	unixtimeStart := deps.now().Unix()
	_, err := json.Marshal(command)
	result.Steps = model.AddStepToSteps(result.Steps, "json.Marshal", "success", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	if strings.TrimSpace(command.Branch) == "" {
		result.Steps = model.AddStepToSteps(result.Steps, "check branch", "", fmt.Errorf("не задана ветка юнита"), deps.now().Unix())
		result.Success = 0
		return result, nil
	}

	if !isUnitPathSegment(command.ProjectId) {
		result.Steps = model.AddStepToSteps(result.Steps, "check project id", "", fmt.Errorf("некорректный ProjectId %q", command.ProjectId), deps.now().Unix())
		result.Success = 0
		return result, nil
	}

	filepathUnit := "./projects/" + command.ProjectId + "/units/" + command.Id

	unixtimeStart = deps.now().Unix()
	err = deps.mkdirAll(filepathUnit, os.ModePerm)
	result.Steps = model.AddStepToSteps(result.Steps, "MkdirAll unit dir", filepathUnit, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	currentPath, err := deps.getwd()
	if err != nil {
		result.Steps = model.AddStepToSteps(result.Steps, "Getwd", "", err, deps.now().Unix())
		result.Success = 0
		return result, nil
	}

	// Раскладка seed-репозитория проекта остаётся прежней: mainBranch.
	projectPath := filepath.Join(currentPath, "projects", command.ProjectId, "mainBranch")

	// Идентичность раннера и адрес backend-а: только config.json (RunnerID
	// обязателен) плюс существующий override RUNNER_BACKEND_URL. Иных
	// fallback/env-переменных не вводим.
	configuration, err := deps.parseConfig()
	result.Steps = model.AddStepToSteps(result.Steps, "read config.json", "", err, deps.now().Unix())
	if err != nil {
		result.Success = 0
		return result, fmt.Errorf("прочитать config.json: %w", err)
	}

	runnerID := strings.TrimSpace(command.RunnerId)
	if runnerID == "" {
		runnerID = strings.TrimSpace(configuration.RunnerID)
	}
	if runnerID == "" {
		err = fmt.Errorf("не задан runnerId: выполните runner init или передайте RunnerId в команде")
		result.Steps = model.AddStepToSteps(result.Steps, "resolve runner id", "", err, deps.now().Unix())
		result.Success = 0
		return result, err
	}

	baseURL := strings.TrimSpace(deps.getenv(runnerBackendURLEnv))
	if baseURL == "" {
		baseURL = strings.TrimSpace(configuration.ApiHost)
	}
	if baseURL == "" {
		err = fmt.Errorf("не задан адрес backend-а: ApiHost в config.json пуст")
		result.Steps = model.AddStepToSteps(result.Steps, "resolve backend url", "", err, deps.now().Unix())
		result.Success = 0
		return result, err
	}

	// Per-runner секрет берётся из config.json вместе с runnerId.
	secret := strings.TrimSpace(configuration.RunnerSecret)
	if secret == "" {
		err = fmt.Errorf("RunnerSecret не задан в config.json")
		result.Steps = model.AddStepToSteps(result.Steps, "read config.json (RunnerSecret)", "", err, deps.now().Unix())
		result.Success = 0
		return result, err
	}

	buildStateClient := deps.newBuildStateClient(baseURL, runnerID, secret, deps.httpClient())

	unixtimeStart = deps.now().Unix()
	buildState, err := buildStateClient.GetBuildState(ctx, command.ProjectId)
	result.Steps = model.AddStepToSteps(result.Steps, "check project build state", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, wrapProjectBuildStateError("проверить состояние сборки проекта", err)
	}

	if !buildState.Built {
		// Проекта ещё нет на этом раннере — клонируем основную ветку проекта в
		// seed-репозиторий и только после успешного clone фиксируем факт сборки.
		unixtimeStart = deps.now().Unix()
		commit, mainBranch, err := deps.ensureProjectRepo(ctx, projectPath, command.StorageUrl)
		response := ""
		if err == nil {
			response = "commit=" + commit
			if mainBranch != "" {
				response += " branch=" + mainBranch
			}
		}
		result.Steps = model.AddStepToSteps(result.Steps, "clone project main branch", response, err, unixtimeStart)
		if err != nil {
			result.Success = 0
			return result, fmt.Errorf("собрать проект %q на раннере %q: %w", command.ProjectId, runnerID, err)
		}

		builtAt := deps.now().Unix()
		record := ProjectBuildRecord{BuiltAt: &builtAt}
		if commit != "" {
			record.Commit = &commit
		}
		if mainBranch != "" {
			record.Branch = &mainBranch
		}

		unixtimeStart = deps.now().Unix()
		_, err = buildStateClient.RecordBuildState(ctx, command.ProjectId, record)
		result.Steps = model.AddStepToSteps(result.Steps, "record project build state", "", err, unixtimeStart)
		if err != nil {
			result.Success = 0
			return result, wrapProjectBuildStateError("записать состояние сборки проекта", err)
		}
	} else {
		result.Steps = model.AddStepToSteps(result.Steps, "project build state", "already built: skip clone", nil, deps.now().Unix())
	}

	// StorageUrl содержит admin-токен. origin переключаем на него только на
	// время fetch, чтобы не ломать приватные репозитории; после fetch токен
	// удаляется из .git/config.
	if strings.TrimSpace(command.StorageUrl) != "" {
		unixtimeStart = deps.now().Unix()
		args := []string{"git", "remote", "set-url", "origin", command.StorageUrl}
		msg, err := deps.execCommand(projectPath, args)
		stepCommand := "git remote set-url origin " + RedactStorageURL(command.StorageUrl)
		result.Steps = model.AddStepToSteps(result.Steps, stepCommand, msg, err, unixtimeStart)
		if err != nil {
			result.Success = 0
			return result, nil
		}
	}

	branchRef := "refs/remotes/origin/" + command.Branch

	unixtimeStart = deps.now().Unix()
	args := []string{"git", "fetch", "--prune", "origin", "+refs/heads/" + command.Branch + ":" + branchRef}
	msg, err := deps.execCommand(projectPath, args)
	result.Steps = model.AddStepToSteps(result.Steps, strings.Join(args, " "), msg, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	unixtimeStart = deps.now().Unix()
	configCommand := "if git cat-file -e " + branchRef + ":unitman.yaml 2>/dev/null; then git show " + branchRef + ":unitman.yaml; fi"
	configOutput, err := deps.execCommand(projectPath, []string{"sh", "-c", configCommand})
	result.Steps = model.AddStepToSteps(result.Steps, configCommand, configOutput, err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	// После fetch токен в origin больше не нужен.
	unixtimeStart = deps.now().Unix()
	err = sanitizeProjectOrigin(projectPath, deps.execCommand)
	result.Steps = model.AddStepToSteps(result.Steps, "strip origin credentials", "", err, unixtimeStart)
	if err != nil {
		result.Success = 0
		return result, nil
	}

	result.Config = configOutput

	return result, nil
}

// wrapProjectBuildStateError делает ошибки build-state понятными и сохраняет
// различимость runner.not_found / runner.inactive / auth.
func wrapProjectBuildStateError(action string, err error) error {
	apiErr, ok := AsProjectBuildStateAPIError(err)
	if !ok {
		return fmt.Errorf("%s: %w", action, err)
	}

	switch {
	case apiErr.IsRunnerNotFound():
		return fmt.Errorf("%s: раннер не найден (runner.not_found): %w", action, err)
	case apiErr.IsRunnerInactive():
		return fmt.Errorf("%s: раннер неактивен (runner.inactive): %w", action, err)
	case apiErr.IsInvalidSignature():
		return fmt.Errorf("%s: неверная подпись (runner.invalid_signature, проверьте RunnerSecret в config.json): %w", action, err)
	case apiErr.IsAuthDisabled():
		return fmt.Errorf("%s: callback-аутентификация выключена на backend (runner.auth_disabled): %w", action, err)
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
}
