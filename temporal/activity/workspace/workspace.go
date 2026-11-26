package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"runner/config"
	"runner/temporal/activity/unit"
	"runner/temporal/activity/unit/model"
	"runner/temporal/credentials"
	"runner/temporal/utils"

	"go.temporal.io/sdk/activity"
)

// workflowID безопасно достаёт id workflow (ResponseId шагов). Вне Temporal
// контекста возвращает пустую строку.
func workflowID(ctx context.Context) (id string) {
	defer func() {
		if recover() != nil {
			id = ""
		}
	}()

	return activity.GetInfo(ctx).WorkflowExecution.ID
}

const (
	// workspaceTemplatesRelativePath — шаблоны workspace относительно рабочего
	// каталога процесса раннера (аналогично пакету unit).
	workspaceTemplatesRelativePath = "temporal/activity/workspace/docker-dind"
	workspaceComposeTemplate       = "compose.dind.yaml"
	workspaceDockerfileTemplate    = "Dockerfile.temp"

	// workspaceComposeService — имя сервиса docker compose, в котором живёт
	// сам workspace (IDE работает в его network namespace).
	workspaceComposeService = "workspace"
	workspaceDirName        = "workspaces"

	// workspaceContainerDir — каталог, в который смонтирован том app.
	workspaceContainerDir = "/workspace"

	// workspaceReadyFile — флаг готовности entrypoint workspace.
	workspaceReadyFile = "/tmp/.workspace_ready"

	defaultWorkspaceMemoryMB = 3072
	workspaceReadyTimeout    = 3 * time.Minute
	workspaceHealthInterval  = time.Second

	workspaceCodeWorkspaceName = "workspace.code-workspace"
)

// parseConfig — точка подмены для тестов. По умолчанию читает config.json,
// из которого берётся адрес реестра, выданный при регистрации раннера.
var parseConfig = config.ParseConfig

// fetchWorkspaceRepoGrant — точка подмены для тестов активностей.
var fetchWorkspaceRepoGrant = credentials.FetchFromConfig

// Project — проект workspace из контракта ZapustitWorkspaceActivity.
type Project struct {
	ProjectId   string
	ProjectName string
	ProjectCode string
	// StorageUrl — чистый remote URL без токена.
	StorageUrl string
	// RepoId нужен для just-in-time запроса гранта.
	RepoId  string
	CanPush bool
	// MainBranch — основная ветка проекта (снимок из Unitman). Нужна скрипту
	// repo-sync, чтобы создать ветку workspace локально, если её нет в origin.
	MainBranch string
}

// ProjectRef — ссылка на проект для проверки изменений.
type ProjectRef struct {
	ProjectId   string
	ProjectName string
}

// ResultatZapuskaWorkspace — результат запуска workspace (раздел 6.4).
type ResultatZapuskaWorkspace struct {
	Success    int
	Steps      []model.Step
	ResponseId string
	Projects   []ProjectResult
}

// ProjectResult — результат подготовки одного проекта.
type ProjectResult struct {
	ProjectId string
	Name      string
	Success   bool
	ErrorKey  *string
}

// ResultatOperaciiWorkspace — общий результат stop/delete.
type ResultatOperaciiWorkspace struct {
	Success    int
	Steps      []model.Step
	ResponseId string
}

// ResultatProverkiWorkspace — результат проверки незакоммиченных изменений.
type ResultatProverkiWorkspace struct {
	Success  bool
	Projects []ProjectIzmeneniya
}

// ProjectIzmeneniya — изменения одного проекта.
type ProjectIzmeneniya struct {
	ProjectId string
	Name      string
	Dirty     bool
	Files     []string
}

// ZapustitWorkspaceCommand — вход ZapustitWorkspaceActivity (раздел 6.3).
type ZapustitWorkspaceCommand struct {
	WorkspaceId   string
	WorkspaceName string
	WorkspaceSlug string
	Branch        string
	ReadOnly      bool
	MemoryLimit   int
	Projects      []Project
}

// WorkspaceRef — вход Ostanovit/Udalit.
type WorkspaceRef struct {
	WorkspaceId string
}

// ProveritIzmeneniyaWorkspaceCommand — вход ProveritIzmeneniyaWorkspaceActivity.
type ProveritIzmeneniyaWorkspaceCommand struct {
	WorkspaceId string
	Projects    []ProjectRef
}

// WorkspaceVolumes — именованные тома конкретного workspace.
type WorkspaceVolumes struct {
	App      string
	Docker   string
	OpenCode string
}

// WorkspaceVolumes возвращает имена томов по контракту раздела 6.1.
func WorkspaceVolumesFor(workspaceID string) WorkspaceVolumes {
	return WorkspaceVolumes{
		App:      "workspace_" + workspaceID + "_app",
		Docker:   "workspace_" + workspaceID + "_docker",
		OpenCode: "workspace_" + workspaceID + "_opencode",
	}
}

// Names возвращает все имена томов workspace.
func (v WorkspaceVolumes) Names() []string {
	return []string{v.App, v.Docker, v.OpenCode}
}

// projectDirName — каталог проекта внутри /workspace. Ключом служит ProjectId:
// именно его получает ProveritIzmeneniyaWorkspaceActivity, у которого нет
// ProjectCode (раздел 6.5).
func projectDirName(projectID string) string {
	return strings.TrimSpace(projectID)
}

// workRoot — корень раскладки раннера (<root>/workspaces/<WorkspaceId>).
func workRoot() string {
	if root := strings.TrimSpace(os.Getenv("RUNNER_WORK_DIR")); root != "" {
		return root
	}

	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// workspaceDirectory вычисляет каталог workspace.
func workspaceDirectory(workspaceID string) string {
	return filepath.Join(workRoot(), workspaceDirName, workspaceID)
}

// validPathSegment пропускает только один нормальный сегмент пути.
func validPathSegment(segment string) bool {
	switch segment {
	case "", ".", "..":
		return false
	}
	if strings.ContainsAny(segment, `/\`) {
		return false
	}
	return filepath.Base(segment) == segment
}

func validateWorkspaceID(workspaceID string) error {
	if !validPathSegment(workspaceID) {
		return fmt.Errorf("некорректный WorkspaceId %q", workspaceID)
	}
	return nil
}

// shellQuote экранирует значение для sh -c.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// workspaceCommand собирает argv docker compose exec для workspace.
func workspaceCommand(command string, workDir string, env []string) []string {
	args := []string{"docker", "compose", "exec", "-T"}
	if prepared := strings.TrimSpace(workDir); prepared != "" {
		args = append(args, "--workdir", prepared)
	}
	for _, entry := range env {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		args = append(args, "-e", entry)
	}
	return append(args, workspaceComposeService, "sh", "-c", command)
}

// execWorkspace выполняет команду внутри контейнера workspace.
func execWorkspace(ctx context.Context, dir, command, workDir string, env []string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("команда для workspace пуста")
	}
	return utils.ExecCommandContext(ctx, dir, workspaceCommand(command, workDir, env))
}

// stepTracker накапливает шаги и публикует их через существующий saveSteps
// с ResponseId = workflowId.
type stepTracker struct {
	responseID string
	steps      []model.Step
}

func newStepTracker(responseID string) *stepTracker {
	return &stepTracker{responseID: responseID, steps: []model.Step{}}
}

func (t *stepTracker) add(command, response string, err error, started time.Time) {
	t.steps = model.AddStepToSteps(t.steps, command, response, err, started.Unix())
}

// publish отправляет снапшот шагов. Пустой снапшот не отправляется: saveSteps
// раннера требует хотя бы один шаг.
func (t *stepTracker) publish(finished bool) {
	if len(t.steps) == 0 {
		return
	}
	unit.SaveStepsToMono(unit.SaveStepsCommand{
		ResponseId: t.responseID,
		Steps:      &t.steps,
		Finished:   finished,
	})
}

// stepsCopy возвращает копию накопленных шагов.
func (t *stepTracker) stepsCopy() []model.Step {
	return append([]model.Step{}, t.steps...)
}

// prepareWorkspaceFiles копирует шаблоны, собирает compose.yaml, Dockerfile и .env.
func prepareWorkspaceFiles(
	workspaceDir string,
	command ZapustitWorkspaceCommand,
	tracker *stepTracker,
) error {
	currentPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("определить рабочий каталог раннера: %w", err)
	}

	templatesPath := filepath.Join(currentPath, workspaceTemplatesRelativePath)

	started := time.Now()
	args := []string{"cp", "-R", templatesPath + "/.", workspaceDir}
	msg, copyErr := utils.ExecCommand(workspaceDir, args)
	tracker.add(strings.Join(args, " "), strings.TrimSpace(msg), copyErr, started)
	if copyErr != nil {
		return fmt.Errorf("не удалось скопировать шаблоны workspace: %w", copyErr)
	}

	started = time.Now()
	composeTemplate, err := os.ReadFile(filepath.Join(workspaceDir, workspaceComposeTemplate))
	if err == nil {
		err = os.WriteFile(filepath.Join(workspaceDir, "compose.yaml"), composeTemplate, 0644)
	}
	tracker.add("make compose.yaml from "+workspaceComposeTemplate, "", err, started)
	if err != nil {
		return fmt.Errorf("не удалось подготовить compose.yaml: %w", err)
	}

	started = time.Now()
	dockerfile, err := renderDockerfile(filepath.Join(workspaceDir, workspaceDockerfileTemplate))
	if err == nil {
		err = os.WriteFile(filepath.Join(workspaceDir, "Dockerfile"), []byte(dockerfile), 0644)
	}
	tracker.add("make Dockerfile", "", err, started)
	if err != nil {
		return fmt.Errorf("не удалось собрать Dockerfile: %w", err)
	}

	started = time.Now()
	err = writeWorkspaceEnv(workspaceDir, command)
	tracker.add("write .env", "", err, started)
	if err != nil {
		return err
	}

	return nil
}

// renderDockerfile разбирает Dockerfile.temp как go-template и возвращает текст.
// Сейчас шаблон без подстановок, но механизм оставлен для расширения.
func renderDockerfile(path string) (string, error) {
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		return "", err
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, nil); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// writeWorkspaceEnv атомарно пишет .env workspace.
//
// Реестр образов выдаётся backend-ом при регистрации и хранится в config.json.
// Нормализация к каноническому виду scheme://host[:port] переиспользуется из
// пакета unit, чтобы workspace и unit трактовали адрес одинаково. Fallback на
// прежние имена реестра запрещён.
func writeWorkspaceEnv(workspaceDir string, command ZapustitWorkspaceCommand) error {
	if strings.ContainsAny(command.WorkspaceName, "\r\n") {
		return errors.New("имя workspace содержит перевод строки")
	}

	configuration, err := parseConfig()
	if err != nil {
		return fmt.Errorf("не удалось прочитать config.json: %w", err)
	}

	registryHost, _, err := unit.ResolveRegistry(configuration.RegistryHost)
	if err != nil {
		return err
	}

	memory := command.MemoryLimit
	if memory <= 0 {
		memory = defaultWorkspaceMemoryMB
	}

	volumes := WorkspaceVolumesFor(command.WorkspaceId)
	readOnly := "false"
	if command.ReadOnly {
		readOnly = "true"
	}

	entries := [][2]string{
		{"UNITMAN_WORKSPACE_ID", command.WorkspaceId},
		{"UNITMAN_WORKSPACE_NAME", command.WorkspaceName},
		{"UNITMAN_WORKSPACE_SLUG", command.WorkspaceSlug},
		{"UNITMAN_BRANCH_NAME", command.Branch},
		{"UNITMAN_READ_ONLY", readOnly},
		{"UNITMAN_MEMORY_LIMIT", strconv.Itoa(memory) + "m"},
		{"UNITMAN_REGISTRY_HOST", registryHost},
		{"COMPOSE_PROJECT_NAME", "workspace_" + command.WorkspaceId},
		{"UNITMAN_WORKSPACE_APP_VOLUME", volumes.App},
		{"UNITMAN_WORKSPACE_DOCKER_VOLUME", volumes.Docker},
		{"UNITMAN_WORKSPACE_OPENCODE_VOLUME", volumes.OpenCode},
	}

	builder := strings.Builder{}
	for _, entry := range entries {
		if strings.ContainsAny(entry[0], "=\r\n") {
			return fmt.Errorf("некорректное имя переменной окружения: %q", entry[0])
		}
		builder.WriteString(entry[0])
		builder.WriteString("=")
		builder.WriteString(entry[1])
		builder.WriteString("\n")
	}

	return os.WriteFile(filepath.Join(workspaceDir, ".env"), []byte(builder.String()), 0644)
}

// ensureVolumes создаёт external-тома workspace (идемпотентно).
func ensureVolumes(workspaceDir string, workspaceID string, tracker *stepTracker) error {
	for _, name := range WorkspaceVolumesFor(workspaceID).Names() {
		started := time.Now()
		args := []string{"docker", "volume", "create", name}
		msg, err := utils.ExecCommand(workspaceDir, args)
		tracker.add(strings.Join(args, " "), strings.TrimSpace(msg), err, started)
		if err != nil {
			return fmt.Errorf("не удалось создать том %s: %w", name, err)
		}
	}
	return nil
}

// waitWorkspaceReady ждёт готовности dockerd и каталога /workspace.
func waitWorkspaceReady(ctx context.Context, workspaceDir string) error {
	deadline := time.Now().Add(workspaceReadyTimeout)
	check := "test -f " + workspaceReadyFile + " && test -S /var/run/docker.sock && docker info >/dev/null 2>&1"

	var lastErr error
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, err := execWorkspace(ctx, workspaceDir, check, "", nil)
		if err == nil {
			return nil
		}
		lastErr = err

		if time.Now().After(deadline) {
			return fmt.Errorf("workspace не стал готов за %s: %w", workspaceReadyTimeout, lastErr)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(workspaceHealthInterval):
		}
	}
}

// waitIDEHealthy ждёт healthcheck контейнера IDE.
func waitIDEHealthy(ctx context.Context, workspaceDir, slug string) error {
	if strings.TrimSpace(slug) == "" {
		return nil
	}

	containerName := slug + ".ide"
	deadline := time.Now().Add(workspaceReadyTimeout)

	var lastStatus string
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		msg, err := utils.ExecCommandContext(ctx, workspaceDir, []string{
			"docker", "inspect", "--format", "{{.State.Health.Status}}", containerName,
		})
		status := strings.TrimSpace(msg)
		if err == nil && status == "healthy" {
			return nil
		}
		lastStatus = status

		if time.Now().After(deadline) {
			return fmt.Errorf("IDE %s не стал healthy за %s (последний статус: %q, ошибка: %v)", containerName, workspaceReadyTimeout, lastStatus, err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(workspaceHealthInterval):
		}
	}
}

// buildRepoEnv собирает переменные окружения для workspace-repo-sync.
// Вынесено отдельно, чтобы контракт окружения можно было покрыть тестами.
func buildRepoEnv(command ZapustitWorkspaceCommand, project Project) []string {
	env := []string{
		"UNITMAN_WORKSPACE_DIR=" + workspaceContainerDir,
		"UNITMAN_PROJECT_ID=" + projectDirName(project.ProjectId),
		"UNITMAN_PROJECT_CODE=" + project.ProjectCode,
		"UNITMAN_REPO_URL=" + project.StorageUrl,
		"UNITMAN_REPO_BRANCH=" + command.Branch,
		"UNITMAN_READ_ONLY=" + strconv.FormatBool(command.ReadOnly || !project.CanPush),
	}

	// Базовую ветку передаём только если она известна: при пустом значении
	// скрипт сам клонирует дефолтную ветку origin.
	if base := strings.TrimSpace(project.MainBranch); base != "" {
		env = append(env, "UNITMAN_REPO_BASE_BRANCH="+base)
	}

	return env
}

// syncProject клонирует/обновляет проект внутри workspace.
func syncProject(
	ctx context.Context,
	workspaceDir string,
	command ZapustitWorkspaceCommand,
	project Project,
	grant credentials.Grant,
) (string, error) {
	cleanURL, err := unit.CleanRepoURL(project.StorageUrl)
	if err != nil {
		return "", err
	}
	project.StorageUrl = cleanURL

	env := buildRepoEnv(command, project)

	credEnv, cleanup, err := unit.InstallGitCredentialsForUnit(
		ctx,
		workspaceDir,
		workspaceComposeService,
		grant,
	)
	if err != nil {
		return "", err
	}
	defer cleanup()

	env = append(env, credEnv...)

	return execWorkspace(ctx, workspaceDir, "/usr/local/bin/workspace-repo-sync", workspaceContainerDir, env)
}

// errorKeyFromOutput достаёт ключ ошибки из вывода скрипта.
func errorKeyFromOutput(output string) string {
	const marker = "UNITMAN_ERROR_KEY="
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, marker) {
			return strings.TrimSpace(strings.TrimPrefix(line, marker))
		}
	}
	return ""
}

// codeWorkspaceFolder — папка multi-root workspace.
type codeWorkspaceFolder struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type codeWorkspace struct {
	Folders []codeWorkspaceFolder `json:"folders"`
}

// generateCodeWorkspace создаёт /workspace/workspace.code-workspace с папками
// проектов.
func generateCodeWorkspace(ctx context.Context, workspaceDir string, projects []Project, tracker *stepTracker) error {
	folders := make([]codeWorkspaceFolder, 0, len(projects))
	for _, project := range projects {
		dir := projectDirName(project.ProjectId)
		if dir == "" {
			continue
		}

		name := strings.TrimSpace(project.ProjectName)
		if name == "" {
			name = dir
		}

		folders = append(folders, codeWorkspaceFolder{
			Path: workspaceContainerDir + "/" + dir,
			Name: name,
		})
	}

	raw, err := json.MarshalIndent(codeWorkspace{Folders: folders}, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось сериализовать workspace.code-workspace: %w", err)
	}

	target := workspaceContainerDir + "/" + workspaceCodeWorkspaceName
	const marker = "UNITMAN_CODE_WORKSPACE_EOF"
	content := string(raw) + "\n"
	command := "cat > " + shellQuote(target) + " <<'" + marker + "'\n" + content + marker

	started := time.Now()
	msg, err := execWorkspace(ctx, workspaceDir, command, workspaceContainerDir, nil)
	tracker.add("generate "+workspaceCodeWorkspaceName, strings.TrimSpace(msg), err, started)
	if err != nil {
		return fmt.Errorf("не удалось создать %s: %w", workspaceCodeWorkspaceName, err)
	}

	return nil
}
