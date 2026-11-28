package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"runner/temporal/activity/unit"
	"runner/temporal/activity/unit/model"

	"go.temporal.io/sdk/activity"
)

const (
	cliProviderID = "unitman"
	cliModelAlias = "main"
	// unitContainerAppDir — каталог приложения внутри контейнера юнита
	// (Dockerfile юнита копирует ./app в /app).
	unitContainerAppDir = "/app"
	// workspaceContainerDir — корень workspace внутри его контейнера.
	workspaceContainerDir = "/workspace"
	// workspaceComposeService — имя compose-сервиса workspace-стенда
	// (раскладка temporal/activity/workspace: workspaceComposeService).
	workspaceComposeService = "workspace"

	// containerHome — HOME контейнера: unit-skills-sync кладёт сюда
	// .config/opencode/skills-index.md. И unit, и workspace работают от root.
	containerHome = "/root"
	// skillsIndexPath — гарантированный instructions-файл для OpenCode.
	skillsIndexPath = containerHome + "/.config/opencode/skills-index.md"
	// skillSyncCommand — best-effort материализация навыков перед ходом.
	skillSyncCommand = "unit-skills-sync --if-changed"
)

// CLIExecutor выполняет ход через установленный в контейнере юнита OpenCode
// CLI. В отличие от HTTPExecutor, CLI сам даёт модели доступ к shell и файлам
// рабочего каталога.
type CLIExecutor struct {
	// ExecUnit запускает команду в контейнере юнита. nil означает реальный
	// docker compose exec; поле нужно для тестов.
	ExecUnit func(ctx context.Context, unitPath, command string, opts unit.UnitExecOptions) (string, error)
	// OnSkillSync — необязательный колбэк для тестов/диагностики: получает
	// ошибку pre-turn синхронизации навыков (nil при успехе).
	OnSkillSync func(err error)
}

func NewCLIExecutor() *CLIExecutor {
	return &CLIExecutor{}
}

// Execute выполняет один ход OpenCode CLI. Первый ход создаёт сессию, следующие
// продолжают её через --session; историю из payload'а передаём только при
// создании новой сессии.
func (e *CLIExecutor) Execute(ctx context.Context, req OpenCodeRequest) (OpenCodeAnswer, error) {
	if strings.TrimSpace(req.URL) == "" {
		return OpenCodeAnswer{}, errors.New("opencode: URL is empty")
	}
	if strings.TrimSpace(req.Model) == "" {
		return OpenCodeAnswer{}, errors.New("opencode: model is empty")
	}
	if strings.TrimSpace(req.APIKey) == "" {
		return OpenCodeAnswer{}, errors.New("opencode: api key is empty")
	}
	if strings.TrimSpace(req.WorkDir) == "" {
		return OpenCodeAnswer{}, errors.New("opencode: unit directory is empty")
	}
	if err := ctx.Err(); err != nil {
		return OpenCodeAnswer{}, err
	}

	answer, output, err := e.execute(ctx, req, strings.TrimSpace(req.SessionID) != "")
	if err != nil && req.SessionID != "" && sessionNotFound(output) {
		// Контейнер мог перезапуститься и потерять локальную сессию OpenCode.
		// В этом случае повторяем ход как первый, с историей из payload'а.
		return e.executeFresh(ctx, req)
	}
	return answer, err
}

func (e *CLIExecutor) executeFresh(ctx context.Context, req OpenCodeRequest) (OpenCodeAnswer, error) {
	answer, _, err := e.execute(ctx, req, false)
	return answer, err
}

// syncSkills выполняет best-effort синхронизацию навыков перед ходом.
// Синтез не наследует переменные OpenCode-конфига: скрипту они не нужны.
func (e *CLIExecutor) syncSkills(
	ctx context.Context,
	req OpenCodeRequest,
	execUnit func(ctx context.Context, unitPath, command string, opts unit.UnitExecOptions) (string, error),
	opts unit.UnitExecOptions,
) {
	syncOpts := opts
	syncOpts.Env = nil

	output, err := execUnit(ctx, req.WorkDir, skillSyncCommand, syncOpts)
	if ctx.Err() != nil {
		// Отменённый ход не должен оставлять лишний прогресс-шаг.
		return
	}
	if err != nil {
		log.Printf("opencode: pre-turn skills sync failed (non-fatal): %s", redact(err.Error(), req.APIKey))
		e.recordSkillSyncStep(ctx, output, err)
	}
}

// recordSkillSyncStep фиксирует результат синхронизации как информационный
// шаг прогресса, если доступен workflow id. Вне Temporal (тесты) — no-op.
func (e *CLIExecutor) recordSkillSyncStep(ctx context.Context, output string, syncErr error) {
	if e.OnSkillSync != nil {
		e.OnSkillSync(syncErr)
	}

	workflowID := activityWorkflowID(ctx)
	if workflowID == "" {
		return
	}

	now := time.Now().Unix()
	step := model.Step{
		Command:       skillSyncCommand,
		Success:       true,
		Unixtime:      now,
		UnixtimeStart: now,
	}
	if syncErr != nil {
		step.Response = "non-fatal: " + syncErr.Error()
	} else {
		step.Response = strings.TrimSpace(output)
	}

	steps := []model.Step{step}
	unit.SaveStepsToMono(unit.SaveStepsCommand{
		ResponseId: workflowID,
		Steps:      &steps,
		Finished:   false,
	})
}

// activityWorkflowID безопасно достаёт id workflow из Temporal-контекста.
func activityWorkflowID(ctx context.Context) (id string) {
	defer func() {
		if recover() != nil {
			id = ""
		}
	}()

	return activity.GetInfo(ctx).WorkflowExecution.ID
}

func (e *CLIExecutor) execute(ctx context.Context, req OpenCodeRequest, continueSession bool) (OpenCodeAnswer, string, error) {
	config, err := cliConfigJSON(req)
	if err != nil {
		return OpenCodeAnswer{}, "", fmt.Errorf("opencode: cannot build config: %w", err)
	}

	command := cliCommand(req, continueSession)
	opts := unit.UnitExecOptions{
		WorkDir: containerWorkDir(req),
		Env: []string{
			"OPENCODE_CONFIG_CONTENT=" + config,
			"OPENCODE_DISABLE_AUTOUPDATE=1",
		},
	}
	// Workspace живёт в compose-сервисе "workspace", а не "unit".
	if req.Workspace {
		opts.Service = workspaceComposeService
	}

	execUnit := e.ExecUnit
	if execUnit == nil {
		execUnit = unit.ExecInUnitContainerContext
	}

	// Перед ходом материализуем обновлённые навыки. Ошибка не валит ход:
	// OpenCode всё равно прочитает skills-index.md из instructions.
	e.syncSkills(ctx, req, execUnit, opts)

	output, execErr := execUnit(ctx, req.WorkDir, command, opts)
	if ctx.Err() != nil {
		return OpenCodeAnswer{}, output, ctx.Err()
	}

	if strings.TrimSpace(output) == "" && execErr != nil {
		output = execErr.Error()
	}

	answer, parseErr := parseCLIOutput(output)
	if parseErr == nil && strings.TrimSpace(answer.Text) == "" && len(answer.Questions) == 0 {
		parseErr = fmt.Errorf("%w: response has no text", ErrUnexpectedResponse)
	}

	if execErr != nil {
		if parseErr != nil {
			message := strings.TrimSpace(output)
			if message == "" {
				message = execErr.Error()
			}
			return OpenCodeAnswer{}, output, fmt.Errorf("opencode: %s", redact(message, req.APIKey))
		}
		return answer, output, nil
	}

	if parseErr != nil {
		return OpenCodeAnswer{}, output, parseErr
	}

	return answer, output, nil
}

// cliCommand собирает shell-команду для opencode run. Промпт передаётся
// позиционным аргументом и экранируется для POSIX shell.
//
// Каталог работы флагом не передаётся: в opencode v2 у `run` нет `--dir`,
// а рабочий каталог и так задаётся через `docker compose exec --workdir`
// (см. UnitExecOptions.WorkDir в execute()).
func cliCommand(req OpenCodeRequest, continueSession bool) string {
	args := []string{
		"opencode", "run",
		"--format", "json",
		"--auto",
		"--model", cliProviderID + "/" + cliModelAlias,
	}
	if agent := strings.TrimSpace(req.AgentName); agent != "" {
		args = append(args, "--agent", agent)
	}
	if continueSession && strings.TrimSpace(req.SessionID) != "" {
		args = append(args, "--session", req.SessionID)
	}
	args = append(args, "--", shellQuote(cliPrompt(req, continueSession)))

	return strings.Join(args, " ")
}

// containerWorkDir выбирает каталог opencode CLI: /workspace для workspace,
// /app — для юнита.
func containerWorkDir(req OpenCodeRequest) string {
	if req.Workspace {
		return workspaceContainerDir
	}
	return unitContainerAppDir
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// validAgentName проверяет имя агента, которое попадает в ключ JSON-конфига
// и в argv `opencode run --agent`. Совпадает с валидацией бэкенда.
func validAgentName(name string) bool {
	if len(name) == 0 || len(name) > 72 {
		return false
	}

	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return false
		}
	}

	return true
}

// cliPrompt собирает сообщение для CLI. При продолжении сессии OpenCode уже
// хранит историю, поэтому отправляем только текущую реплику.
func cliPrompt(req OpenCodeRequest, continueSession bool) string {
	prompt := cliPromptBase(req, continueSession)
	// Инструкция про артефакты — только у workspace-хода, где задан
	// каталог-исходящий ящик. HTTPExecutor это поле не читает.
	if strings.TrimSpace(req.ArtifactDir) != "" {
		prompt += "\n\n" + cliArtifactInstruction(req.ArtifactDir)
	}
	switch {
	case req.DenyEdits || req.AgentDenyEdits:
		// Read-only ход по юниту или plan-подобный мод: правки файлов
		// запрещены и на уровне permission-конфига, инструкция дублирует
		// запрет словами.
		prompt += "\n\n" + cliReadOnlyInstruction
	case req.ReadOnly:
		// Read-only workspace: правки в рабочей копии разрешены,
		// запись в git запрещена.
		prompt += "\n\n" + cliGitWriteInstruction
	}
	return prompt
}

func cliPromptBase(req OpenCodeRequest, continueSession bool) string {
	messages := req.Messages
	if len(messages) == 0 {
		return cliQuestionInstruction
	}

	current := strings.TrimSpace(messages[len(messages)-1].Text)
	if continueSession || len(messages) == 1 {
		return current + "\n\n" + cliQuestionInstruction
	}

	var builder strings.Builder
	builder.WriteString("История диалога для контекста:\n")
	for _, message := range messages[:len(messages)-1] {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			builder.WriteString("Пользователь: ")
		case "assistant":
			builder.WriteString("Ассистент: ")
		default:
			continue
		}
		builder.WriteString(text)
		builder.WriteString("\n")
	}

	builder.WriteString("\nТекущий запрос:\n")
	builder.WriteString(current)
	builder.WriteString("\n\n")
	builder.WriteString(cliQuestionInstruction)

	return builder.String()
}

const cliQuestionInstruction = `Если для продолжения действительно нужен выбор или уточнение пользователя, не выполняй действие и верни строго один JSON-объект без пояснений и markdown:
{"questions":[{"id":"q1","text":"текст вопроса","allowFreeText":true,"options":[{"id":"o1","label":"вариант","description":"пояснение"}]}]}
Если вопрос допускает свободный ответ, установи allowFreeText = true.`

// cliReadOnlyInstruction — для read-only хода по юниту: правки файлов
// запрещены (и на уровне permission-конфига, см. cliPermission), диагностика
// остаётся доступна (plan §3.4).
const cliReadOnlyInstruction = `Работа идёт в режиме read-only: запрещено изменять файлы, выполнять git commit и git push. Можно читать код, запускать тесты, линтеры и другие диагностические команды.`

// cliGitWriteInstruction — для read-only workspace: правки в рабочей копии
// разрешены, запись в git запрещена.
const cliGitWriteInstruction = `Работа идёт в режиме read-only: запрещено выполнять git commit и git push. Можно менять файлы в рабочей копии, но нельзя создавать коммиты и отправлять их в удалённый репозиторий.`

// cliArtifactInstructionTemplate — инструкция агенту складывать итоговые
// документы в каталог-исходящий ящик. %s — контейнерный путь каталога.
const cliArtifactInstructionTemplate = `Если пользователь просит подготовить план, отчёт, спецификацию или другой документ,
сохрани итоговый файл в %s/<краткое-имя>.md.
Файлы из этого каталога автоматически прикрепляются к твоему ответу.
Если пользователь указал конкретный путь, сохрани файл туда и продублируй его в этот каталог.
Не помещай в каталог секреты, ключи, бинарные файлы и больше 5 файлов.`

func cliArtifactInstruction(dir string) string {
	return fmt.Sprintf(cliArtifactInstructionTemplate, dir)
}

type cliConfig struct {
	Provider map[string]cliProvider `json:"provider"`
	// Agent — выбранный мод агента. Ключ карты — имя агента, оно же
	// передаётся в `opencode run --agent`.
	Agent map[string]cliAgent `json:"agent,omitempty"`
	// Instructions — файл-индекс навыков. unit-skills-sync создаёт его даже
	// при отсутствии навыков, поэтому ссылка безопасна и служит fallback,
	// если OpenCode не обнаруживает каталог skill/ автоматически.
	Instructions []string       `json:"instructions,omitempty"`
	Permission   *cliPermission `json:"permission,omitempty"`
}

// cliAgent описывает inline-агента OpenCode. Промпт полностью заменяет
// системный промпт агента; tools остаются дефолтными.
type cliAgent struct {
	Mode       string         `json:"mode"`
	Prompt     string         `json:"prompt,omitempty"`
	Permission *cliPermission `json:"permission,omitempty"`
}

// cliPermission — жёсткое ограничение прав read-only хода на уровне
// opencode, а не только словами в промпте (plan §3.4).
type cliPermission struct {
	// Edit запрещает инструменты правки файлов (edit/write/patch).
	Edit string `json:"edit,omitempty"`
	// Bash точечно запрещает запись в git; команды, не совпавшие ни с
	// одним паттерном, остаются разрешёнными по умолчанию — диагностика
	// (тесты, линтеры) доступна.
	Bash map[string]string `json:"bash,omitempty"`
}

// cliGitWriteDeny — bash-правила записи в git для read-only хода. Паттерны
// заданы без `*`-fallback, чтобы при любом порядке разбора паттернов в
// opencode всё, кроме git-записи, осталось разрешённым по умолчанию.
var cliGitWriteDeny = map[string]string{
	"git push*":   "deny",
	"git commit*": "deny",
}

type cliProvider struct {
	NPM     string              `json:"npm"`
	Name    string              `json:"name"`
	Options cliProviderOptions  `json:"options"`
	Models  map[string]cliModel `json:"models"`
}

type cliProviderOptions struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

type cliModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func cliConfigJSON(req OpenCodeRequest) (string, error) {
	config := cliConfig{
		Provider: map[string]cliProvider{
			cliProviderID: {
				NPM:  "@ai-sdk/openai-compatible",
				Name: "Unitman",
				Options: cliProviderOptions{
					BaseURL: cliBaseURL(req.URL),
					APIKey:  req.APIKey,
				},
				Models: map[string]cliModel{
					cliModelAlias: {
						ID:   req.Model,
						Name: req.Model,
					},
				},
			},
		},
		Instructions: []string{skillsIndexPath},
	}

	// plan-подобный мод запрещает правку файлов в обоих типах чата.
	denyEdits := req.DenyEdits || req.AgentDenyEdits

	if agent := strings.TrimSpace(req.AgentName); agent != "" {
		if !validAgentName(agent) {
			return "", fmt.Errorf("opencode: invalid agent name")
		}
		agentConfig := cliAgent{
			Mode:   "primary",
			Prompt: req.AgentPrompt,
		}
		if req.AgentDenyEdits {
			agentConfig.Permission = &cliPermission{
				Edit: "deny",
				Bash: cliGitWriteDeny,
			}
		}
		config.Agent = map[string]cliAgent{agent: agentConfig}
	}

	// Read-only ход ограничивается на уровне opencode, а не только
	// инструкцией в промпте (plan §3.4): запись в git запрещена у любого
	// read-only хода, правка файлов — дополнительно у read-only хода по
	// юниту или plan-подобного мода.
	if req.ReadOnly || denyEdits {
		config.Permission = &cliPermission{
			Bash: cliGitWriteDeny,
		}
	}

	if denyEdits {
		config.Permission.Edit = "deny"
	}

	raw, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// cliBaseURL принимает и базу API, и полный endpoint chat/completions.
func cliBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	return strings.TrimSuffix(base, "/chat/completions")
}

type cliEvent struct {
	Type      string         `json:"type"`
	SessionID string         `json:"sessionID"`
	Part      cliEventPart   `json:"part"`
	Error     *cliEventError `json:"error"`
}

type cliEventPart struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

type cliEventError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Data    struct {
		Message string `json:"message"`
	} `json:"data"`
}

// parseCLIOutput разбирает NDJSON-поток opencode run --format json. Логи CLI
// могут попадать в общий вывод, поэтому строки без JSON просто пропускаются.
func parseCLIOutput(output string) (OpenCodeAnswer, error) {
	var (
		session   string
		texts     = make(map[string]string)
		order     []string
		eventErr  string
		anonymous int
	)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}

		var event cliEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		if session == "" {
			session = strings.TrimSpace(event.SessionID)
		}

		if event.Type == "error" && event.Error != nil {
			message := strings.TrimSpace(event.Error.Data.Message)
			if message == "" {
				message = strings.TrimSpace(event.Error.Message)
			}
			if message == "" {
				message = strings.TrimSpace(event.Error.Name)
			}
			if message != "" {
				eventErr = message
			}
			continue
		}

		if event.Type != "text" {
			continue
		}

		id := strings.TrimSpace(event.Part.ID)
		if id == "" {
			anonymous++
			id = fmt.Sprintf("anonymous-%d", anonymous)
		}
		if _, ok := texts[id]; !ok {
			order = append(order, id)
		}
		if text := strings.TrimSpace(event.Part.Text); text != "" {
			texts[id] = event.Part.Text
		}
	}

	var builder strings.Builder
	for _, id := range order {
		builder.WriteString(texts[id])
	}
	text := strings.TrimSpace(builder.String())

	if eventErr != "" {
		return OpenCodeAnswer{}, fmt.Errorf("opencode: %s", eventErr)
	}

	questions := make([]Vopros, 0)
	if structured, structuredAnswer := parseStructuredContent(text); len(structured) > 0 || structuredAnswer != "" {
		questions = structured
		if structuredAnswer != "" {
			text = structuredAnswer
		}
	}

	answer := OpenCodeAnswer{Text: text, Questions: questions}
	if session != "" {
		answer.SessionID = &session
	}

	return answer, nil
}

// sessionNotFound распознаёт ошибку продолжения потерянной сессии OpenCode.
func sessionNotFound(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"session not found",
		"no such session",
		"unknown session",
		"session does not exist",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
