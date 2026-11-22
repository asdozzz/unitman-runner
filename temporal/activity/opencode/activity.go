package opencode

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	unitActivity "runner/temporal/activity/unit"

	"go.temporal.io/sdk/activity"
)

// ActivityName — литеральное имя активности. Workflow на стороне бэкенда
// вызывает её именно так, поэтому имя нельзя выводить из имени метода.
const ActivityName = "VipolnitHodOpenCodeActivity"

const (
	KindAnswer   = "ANSWER"
	KindQuestion = "QUESTION"
	KindError    = "ERROR"
)

const (
	ErrorKeyTurnFailed            = "opencode.turn.failed"
	ErrorKeyUnexpectedKind        = "opencode.turn.unexpected_kind"
	ErrorKeyUnitDirectoryNotFound = "opencode.turn.unit_directory_not_found"
)

const (
	// turnBudget держит ход заметно ниже 15 минут, которые живёт workflow.
	turnBudget = 13 * time.Minute

	maxStepsBytes     = 2560 * 1024
	stepResponseLimit = 16 * 1024
	throttle          = 250 * time.Millisecond

	progressRequestTimeout = 5 * time.Second
	progressAttempts       = 3
	progressRetryDelay     = 200 * time.Millisecond
)

// ─── вход ────────────────────────────────────────────────────────────────────

// Otvet — структурированный ответ пользователя на вопрос предыдущего хода.
type Otvet struct {
	QuestionID string  `json:"QuestionId"`
	OptionID   *string `json:"OptionId"`
	Text       *string `json:"Text"`
}

// Replika — одна реплика истории диалога.
type Replika struct {
	Role string `json:"Role"` // "user" | "assistant"
	Text string `json:"Text"`
}

// UnitRef — юнит, к которому привязана сессия. Бэкенд передаёт только
// идентификаторы: каталог юнита вычисляет раннер по своей схеме раскладки.
type UnitRef struct {
	ProjectID string `json:"ProjectId"`
	UnitID    string `json:"UnitId"`
}

// WorkspaceProject — проект workspace-сессии. ProjectCode нужен бэкенду и
// документации, а каталог вычисляется по ProjectId (как и у Proverit).
type WorkspaceProject struct {
	ProjectID   string `json:"ProjectId"`
	ProjectName string `json:"ProjectName"`
	ProjectCode string `json:"ProjectCode"`
	CanPush     bool   `json:"CanPush"`
}

// HodCommand — payload активности. SessionId приходит как null на первом ходе
// и разворачивается в пустую строку.
//
// Units — упорядоченный список юнитов сессии: Units[0] задаёт рабочий каталог
// хода, Units[1..] — дополнительные корни workspace. Пустой список означает
// свободную сессию. ProjectID/UnitID продублированы для обратной совместимости
// и равны Units[0].
type HodCommand struct {
	SessionID string    `json:"SessionId"`
	Prompt    string    `json:"Prompt"`
	Answers   []Otvet   `json:"Answers"`
	History   []Replika `json:"History"`
	URL       string    `json:"Url"`
	Model     string    `json:"Model"`
	ApiKey    string    `json:"ApiKey"`
	Units     []UnitRef `json:"Units"`
	ProjectID *string   `json:"ProjectId"`
	UnitID    *string   `json:"UnitId"`

	// WorkspaceID != "" переключает ход на workspace: cwd —
	// <base>/workspaces/<WorkspaceID>, roots — подкаталоги проектов.
	WorkspaceID       string             `json:"WorkspaceId"`
	WorkspaceProjects []WorkspaceProject `json:"WorkspaceProjects"`
	WorkspaceReadOnly bool               `json:"WorkspaceReadOnly"`

	// ReadOnly — режим хода по юниту (plan §8/§9). Для RW-хода раннер
	// настраивает git-креденшелы из личного токена пользователя Token.
	// Отсутствие полей у старых бэкендов означает readOnly=false, token="".
	ReadOnly bool   `json:"ReadOnly"`
	Token    string `json:"Token"`
	// UserName/UserEmail — необязательная git-идентичность (15.12-а).
	UserName  string `json:"UserName"`
	UserEmail string `json:"UserEmail"`
}

// MarshalJSON сохраняет массивы пустыми массивами, а не null: этого требует
// контракт payload'а.
func (c HodCommand) MarshalJSON() ([]byte, error) {
	type plain HodCommand
	if c.Answers == nil {
		c.Answers = []Otvet{}
	}
	if c.History == nil {
		c.History = []Replika{}
	}
	if c.Units == nil {
		c.Units = []UnitRef{}
	}
	if c.WorkspaceProjects == nil {
		c.WorkspaceProjects = []WorkspaceProject{}
	}
	return json.Marshal(plain(c))
}

// ─── выход ───────────────────────────────────────────────────────────────────

// Variant — вариант выбора в вопросе.
type Variant struct {
	ID          string  `json:"Id"`
	Label       string  `json:"Label"`
	Description *string `json:"Description,omitempty"`
}

// Vopros — вопрос, который OpenCode задал пользователю.
type Vopros struct {
	ID            string    `json:"Id"`
	Text          string    `json:"Text"`
	AllowFreeText bool      `json:"AllowFreeText"`
	Options       []Variant `json:"Options"`
}

// HodResult — результат одного хода. Questions всегда сериализуется как
// массив (не null).
//
// ErrorMessage — фактический текст ошибки раннера/OpenCode. В отличие от
// ErrorKey он не переводится и нужен, чтобы пользователь увидел причину,
// а не общий «ход завершился ошибкой».
type HodResult struct {
	Success           bool     `json:"Success"`
	Kind              string   `json:"Kind"`
	Questions         []Vopros `json:"Questions"`
	Answer            *string  `json:"Answer"`
	ErrorKey          *string  `json:"ErrorKey"`
	ErrorMessage      *string  `json:"ErrorMessage"`
	ExternalSessionID *string  `json:"ExternalSessionId"`
}

// MarshalJSON гарантирует, что Questions уходит в бэкенд массивом, а не null,
// даже если результат собран не через конструктор errorResult.
func (r HodResult) MarshalJSON() ([]byte, error) {
	type plain HodResult
	if r.Questions == nil {
		r.Questions = []Vopros{}
	}
	return json.Marshal(plain(r))
}

func errorResult(key string) HodResult {
	k := key
	return HodResult{
		Success:   false,
		Kind:      KindError,
		Questions: []Vopros{},
		ErrorKey:  &k,
	}
}

// errorResultWithMessage добавляет к ошибке фактический текст причины.
func errorResultWithMessage(key, message string) HodResult {
	result := errorResult(key)
	message = strings.TrimSpace(message)
	if message != "" {
		result.ErrorMessage = &message
	}
	return result
}

// ─── активность ──────────────────────────────────────────────────────────────

// Activities — набор активностей OpenCode, регистрируемых на worker'е.
type Activities struct {
	BackendURL string
	Secret     string
	Client     *http.Client
	OpenCode   OpenCodeExecutor
	// WorkDir — корень раскладки раннера, внутри которого лежит projects/.
	// Пустое значение означает текущий рабочий каталог процесса.
	WorkDir string
}

// VipolnitHodOpenCodeActivity выполняет ровно один ход диалога с OpenCode.
// Если OpenCode просит выбор, активность сразу возвращает QUESTION и не ждёт
// ответа пользователя.
func (a *Activities) VipolnitHodOpenCodeActivity(ctx context.Context, cmd HodCommand) (result HodResult, err error) {
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	p := newProgressSender(client, a.BackendURL, a.Secret, workflowID(ctx))

	// Именованные возвраты нужны, чтобы паника не утекла в Temporal.
	defer func() {
		if r := recover(); r != nil {
			message := fmt.Sprintf("panic: %s", redact(fmt.Sprint(r), cmd.ApiKey))
			log.Printf("opencode: %s", message)
			p.publish(ctx, true, false)
			result = errorResultWithMessage(ErrorKeyTurnFailed, message)
			err = nil
		}
	}()

	if a.OpenCode == nil {
		log.Printf("opencode: executor is not configured")
		p.publish(ctx, true, false)
		return errorResultWithMessage(ErrorKeyTurnFailed, "opencode executor is not configured"), nil
	}

	// Каталоги юнитов вычисляет раннер: бэкенд передаёт только ProjectId/UnitId.
	// Любой отсутствующий каталог — ошибка хода без частичного запуска.
	workDir, roots, dirErr := a.resolveWorkDirs(cmd)
	if dirErr != nil {
		log.Printf("opencode: cannot resolve workspace: %v", dirErr)
		p.publish(ctx, true, false)
		return errorResultWithMessage(ErrorKeyUnitDirectoryNotFound, dirErr.Error()), nil
	}

	// Режим хода по юниту: read-only ход не должен иметь push-креденшелов,
	// RW-ход получает личный токен владельца сессии на время хода. Ошибка
	// настройки git-сессии безопасно деградирует до read-only.
	effectiveReadOnly := cmd.WorkspaceReadOnly || cmd.ReadOnly
	cleanupGitSession := func() {}
	isUnitSession := strings.TrimSpace(cmd.WorkspaceID) == "" &&
		(len(cmd.Units) > 0 || cmd.UnitID != nil)
	if isUnitSession {
		cleanup, ro, gitErr := unitActivity.BeginHodGitSession(
			workDir,
			effectiveReadOnly,
			cmd.Token,
			cmd.UserName,
			cmd.UserEmail,
		)
		cleanupGitSession = cleanup
		if gitErr != nil {
			log.Printf("opencode: cannot configure git session: %s", redact(gitErr.Error(), cmd.Token))
			ro = true
		}
		effectiveReadOnly = ro
	}
	defer cleanupGitSession()

	runCtx, cancel := context.WithTimeout(ctx, turnBudget)
	defer cancel()

	started := time.Now()
	answer, execErr := a.OpenCode.Execute(runCtx, OpenCodeRequest{
		SessionID: cmd.SessionID,
		URL:       cmd.URL,
		Model:     cmd.Model,
		APIKey:    cmd.ApiKey,
		WorkDir:   workDir,
		Roots:     roots,
		Messages:  sobratSoobsheniya(cmd),
		Workspace: strings.TrimSpace(cmd.WorkspaceID) != "",
		ReadOnly:  effectiveReadOnly,
		// Правки файлов запрещены только у read-only хода по юниту
		// (15.7-а): у read-only workspace правки в рабочей копии
		// разрешены, запрещена запись в git.
		DenyEdits: isUnitSession && effectiveReadOnly,
	})

	if execErr != nil {
		// В command и response не должно быть ключа: только модель и текст ошибки.
		p.addStep(ctx, "opencode "+cmd.Model, redact(execErr.Error(), cmd.ApiKey), false, started)
		log.Printf("opencode: turn failed: %s", redact(execErr.Error(), cmd.ApiKey))
	} else {
		p.addStep(ctx, "opencode "+cmd.Model, redact(answer.Text, cmd.ApiKey), true, started)
	}

	if execErr != nil {
		// Активность отменена — финальный прогресс не нужен.
		if ctx.Err() != nil {
			return HodResult{}, ctx.Err()
		}

		// Неразборчивый ответ OpenCode — это отдельный ключ перевода, а не
		// общая ошибка выполнения.
		key := ErrorKeyTurnFailed
		if errors.Is(execErr, ErrUnexpectedResponse) {
			key = ErrorKeyUnexpectedKind
		}

		p.publish(ctx, true, false)

		// execErr в CLI-исполнителе уже содержит stderr/вывод OpenCode.
		// Сохраняем его как факт, чтобы бэкенд и UI показали реальную причину.
		message := obrezatTekst(redact(execErr.Error(), cmd.ApiKey))
		return errorResultWithMessage(key, message), nil
	}

	// Продолжаем уже известную сессию, даже если клиент не вернул её в ответе:
	// без этого следующий ход потеряет контекст.
	if answer.SessionID == nil && strings.TrimSpace(cmd.SessionID) != "" {
		session := cmd.SessionID
		answer.SessionID = &session
	}

	result = prevratitRezultat(answer)
	sanitizeResult(&result, cmd.ApiKey)
	p.publish(ctx, true, result.Kind != KindError)

	return result, nil
}

// workflowID безопасно достаёт id workflow, который является токеном прогресса.
// Вне Temporal-контекста GetInfo паникует — тогда прогресс просто отключён.
func workflowID(ctx context.Context) (id string) {
	defer func() {
		if recover() != nil {
			id = ""
		}
	}()

	return activity.GetInfo(ctx).WorkflowExecution.ID
}

func sobratSoobsheniya(cmd HodCommand) []Replika {
	msgs := make([]Replika, 0, len(cmd.History)+1)
	msgs = append(msgs, cmd.History...)
	msgs = append(msgs, Replika{Role: "user", Text: cmd.Prompt})
	return msgs
}

// ─── рабочая директория ──────────────────────────────────────────────────────

// resolveWorkDirs собирает рабочие каталоги хода.
//
// Первый возвращённый каталог — cwd хода, остальные — дополнительные корни
// workspace (multi-root), порядок юнитов сохраняется. Пустой Units означает
// свободную сессию: работает в корне проекта, а без ProjectID — в корне раннера.
func (a *Activities) resolveWorkDirs(cmd HodCommand) (cwd string, roots []string, err error) {
	base := a.workRoot()

	// Workspace имеет приоритет над Units/legacy-парой.
	if strings.TrimSpace(cmd.WorkspaceID) != "" {
		return a.resolveWorkspaceDirs(base, cmd)
	}

	units := cmd.Units

	// Обратная совместимость со старыми бэкендами: пара верхнего уровня.
	if len(units) == 0 && (cmd.ProjectID != nil || cmd.UnitID != nil) {
		units = []UnitRef{{ProjectID: deref(cmd.ProjectID), UnitID: deref(cmd.UnitID)}}
	}

	if len(units) == 0 {
		dir, err := a.checkDir(base)
		return dir, nil, err
	}

	roots = make([]string, 0, len(units))
	for _, unit := range units {
		dir, err := a.checkDir(a.unitDir(base, unit))
		if err != nil {
			// Частичный запуск запрещён: любой отсутствующий корень — ошибка хода.
			return "", nil, err
		}
		roots = append(roots, dir)
	}

	return roots[0], roots[1:], nil
}

// resolveWorkspaceDirs вычисляет cwd и roots workspace-хода.
//
// cwd — хостовый каталог <base>/workspaces/<WorkspaceID> с compose.yaml:
// он служит контекстом docker compose exec. Корни — контейнерные пути
// /workspace/<ProjectID>: проекты живут в томе workspace_<id>_app и на хосте
// не видны. CLI executor запускает opencode в /workspace, где все проекты
// уже присутствуют после успешного старта workspace, поэтому хостовая
// проверка подкаталогов проектов не нужна (и невозможна).
func (a *Activities) resolveWorkspaceDirs(base string, cmd HodCommand) (cwd string, roots []string, err error) {
	workspaceID := strings.TrimSpace(cmd.WorkspaceID)
	if !validPathSegment(workspaceID) {
		return "", nil, fmt.Errorf("incorrect workspace id: %q", workspaceID)
	}

	cwd, err = a.checkDir(filepath.Join(base, "workspaces", workspaceID))
	if err != nil {
		return "", nil, err
	}

	roots = make([]string, 0, len(cmd.WorkspaceProjects))
	for _, project := range cmd.WorkspaceProjects {
		projectID := strings.TrimSpace(project.ProjectID)
		if !validPathSegment(projectID) {
			return "", nil, fmt.Errorf("incorrect workspace project id: %q", projectID)
		}

		roots = append(roots, filepath.Join(workspaceContainerDir, projectID))
	}

	return cwd, roots, nil
}

// unitDir вычисляет каталог юнита по существующей схеме раскладки раннера:
// <base>/projects/<ProjectId>/units/<UnitId>. Пустой UnitId — корень проекта.
// Некорректный идентификатор возвращает пустую строку, чтобы checkDir не смог
// выйти за пределы корня (защита от path traversal).
func (a *Activities) unitDir(base string, unit UnitRef) string {
	projectID := strings.TrimSpace(unit.ProjectID)
	if !validPathSegment(projectID) {
		return ""
	}

	dir := filepath.Join(base, "projects", projectID)

	unitID := strings.TrimSpace(unit.UnitID)
	if unitID == "" {
		return dir
	}
	if !validPathSegment(unitID) {
		return ""
	}

	return filepath.Join(dir, "units", unitID)
}

// workRoot — корень, внутри которого лежит projects/.
func (a *Activities) workRoot() string {
	if strings.TrimSpace(a.WorkDir) != "" {
		return a.WorkDir
	}

	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// checkDir проверяет, что каталог существует, и возвращает его абсолютный путь.
func (a *Activities) checkDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("unit directory is not defined")
	}

	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("unit directory is not available: %w", err)
	}

	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("unit directory not found: %s", absolute)
	}

	return absolute, nil
}

// validPathSegment пропускает только один нормальный сегмент пути: без
// разделителей, пустых значений и переходов вверх.
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

// deref разыменовывает необязательную строку: nil → "".
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func prevratitRezultat(a OpenCodeAnswer) HodResult {
	session := a.SessionID
	if session != nil && strings.TrimSpace(*session) == "" {
		session = nil
	}

	switch {
	case len(a.Questions) > 0:
		return HodResult{
			Success:           true,
			Kind:              KindQuestion,
			Questions:         normalizeQuestions(a.Questions),
			ExternalSessionID: session,
		}
	case strings.TrimSpace(a.Text) != "":
		answer := a.Text
		return HodResult{
			Success:           true,
			Kind:              KindAnswer,
			Questions:         []Vopros{},
			Answer:            &answer,
			ExternalSessionID: session,
		}
	default:
		return errorResult(ErrorKeyUnexpectedKind)
	}
}

// normalizeQuestions приводит вопросы к контракту бэкенда: непустые
// уникальные Id, непустые Label, Options всегда массив.
func normalizeQuestions(in []Vopros) []Vopros {
	out := make([]Vopros, 0, len(in))
	usedQuestions := make(map[string]bool, len(in))

	for i, q := range in {
		if strings.TrimSpace(q.ID) == "" {
			q.ID = uniqueID(fmt.Sprintf("q%d", i+1), usedQuestions)
		} else if usedQuestions[q.ID] {
			q.ID = uniqueID(q.ID, usedQuestions)
		} else {
			usedQuestions[q.ID] = true
		}

		if strings.TrimSpace(q.Text) == "" {
			q.Text = q.ID
		}

		if q.Options == nil {
			q.Options = []Variant{}
		} else {
			usedOptions := make(map[string]bool, len(q.Options))
			for j := range q.Options {
				if strings.TrimSpace(q.Options[j].ID) == "" {
					q.Options[j].ID = uniqueID(fmt.Sprintf("o%d", j+1), usedOptions)
				} else if usedOptions[q.Options[j].ID] {
					q.Options[j].ID = uniqueID(q.Options[j].ID, usedOptions)
				} else {
					usedOptions[q.Options[j].ID] = true
				}

				if strings.TrimSpace(q.Options[j].Label) == "" {
					q.Options[j].Label = q.Options[j].ID
				}
			}
		}

		out = append(out, q)
	}

	return out
}

func uniqueID(base string, used map[string]bool) string {
	candidate := base
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	used[candidate] = true
	return candidate
}

// sanitizeResult — последний рубеж защиты: API-ключ не должен попасть в
// результат, даже если модель или ошибка его случайно повторили.
func sanitizeResult(result *HodResult, secret string) {
	if secret == "" || result == nil {
		return
	}

	if result.Answer != nil {
		answer := redact(*result.Answer, secret)
		result.Answer = &answer
	}

	if result.ErrorMessage != nil {
		message := redact(*result.ErrorMessage, secret)
		result.ErrorMessage = &message
	}

	if result.ExternalSessionID != nil {
		session := redact(*result.ExternalSessionID, secret)
		result.ExternalSessionID = &session
	}

	for i := range result.Questions {
		result.Questions[i].ID = redact(result.Questions[i].ID, secret)
		result.Questions[i].Text = redact(result.Questions[i].Text, secret)
		for j := range result.Questions[i].Options {
			option := &result.Questions[i].Options[j]
			option.ID = redact(option.ID, secret)
			option.Label = redact(option.Label, secret)
			if option.Description != nil {
				description := redact(*option.Description, secret)
				option.Description = &description
			}
		}
	}
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// ─── прогресс ────────────────────────────────────────────────────────────────

// Step — один шаг прогресса. Ключи JSON — нижний регистр, как их читает UI.
type Step struct {
	Command       string `json:"command"`
	Response      string `json:"response"`
	Success       bool   `json:"success"`
	Unixtime      int64  `json:"unixtime"`                // время окончания
	UnixtimeStart *int64 `json:"unixtimeStart,omitempty"` // время начала, необязательно
}

type saveStepsRequest struct {
	ResponseID   string `json:"responseId"`
	StepsContent string `json:"stepsContent"`
	Finished     bool   `json:"finished"`
	Success      bool   `json:"success"`
}

type progressSender struct {
	client       *http.Client
	url          string
	secret       string
	token        string
	mu           sync.Mutex
	steps        []Step
	lastPub      time.Time
	finishedSent bool
}

func newProgressSender(c *http.Client, base, secret, token string) *progressSender {
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}

	return &progressSender{
		client: c,
		url:    saveStepsURL(base),
		secret: secret,
		token:  token,
	}
}

// saveStepsURL принимает и корень бэкенда, и базу с уже добавленным /api.
func saveStepsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}

	switch {
	case strings.HasSuffix(base, "/api/runner/saveSteps"):
		return base
	case strings.HasSuffix(base, "/api/runner"):
		return base + "/saveSteps"
	case strings.HasSuffix(base, "/api"):
		return base + "/runner/saveSteps"
	default:
		return base + "/api/runner/saveSteps"
	}
}

// addStep добавляет шаг в снапшот. response — вывод OpenCode либо текст
// ошибки; API-ключа здесь быть не должно (вызывающий обязан его вырезать).
func (p *progressSender) addStep(ctx context.Context, command, response string, ok bool, started time.Time) {
	// После отмены ход должен завершиться сразу, а не ждать сеть.
	if ctx.Err() != nil {
		return
	}

	fin := time.Now().Unix()
	start := started.Unix()
	if start > fin {
		start = fin
	}

	p.mu.Lock()
	p.steps = append(p.steps, Step{
		Command:       command,
		Response:      obrezatTekst(response),
		Success:       ok,
		Unixtime:      fin,
		UnixtimeStart: &start,
	})
	p.mu.Unlock()

	p.publish(ctx, false, ok)
}

// publish отправляет полный снапшот шагов. Ошибки прогресса никогда не
// влияют на ход.
func (p *progressSender) publish(ctx context.Context, finished, success bool) {
	// Прогресс — вспомогательный канал: его паника не должна валить ход.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("opencode: panic in progress sender: %v", r)
		}
	}()

	if p == nil || p.url == "" || p.token == "" {
		return
	}

	p.mu.Lock()
	if p.finishedSent {
		p.mu.Unlock()
		return
	}
	if finished {
		p.finishedSent = true
	} else if time.Since(p.lastPub) < throttle {
		p.mu.Unlock()
		return
	}
	p.lastPub = time.Now()
	// Пустой снапшот тоже должен быть JSON-массивом, а не null.
	steps := append([]Step{}, p.steps...)
	p.mu.Unlock()

	raw, err := json.Marshal(steps)
	if err != nil {
		return
	}
	content := string(raw)
	if len(content) > maxStepsBytes {
		log.Printf("opencode: progress snapshot is too large (%d bytes), skipped", len(content))
		return
	}

	body, err := json.Marshal(saveStepsRequest{
		ResponseID:   p.token,
		StepsContent: content,
		Finished:     finished,
		Success:      success,
	})
	if err != nil {
		return
	}

	reqCtx := ctx
	if ctx.Err() != nil {
		// Контекст уже отменён — финальный снапшот всё равно нужно попробовать
		// отправить, но со своим коротким таймаутом. context.WithoutCancel
		// появился только в Go 1.21, а модуль объявлен как Go 1.18, поэтому
		// берём пустой контекст: значений из исходного здесь не требуется.
		reqCtx = context.Background()
	}

	for attempt := 0; attempt < progressAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(progressRetryDelay):
			case <-reqCtx.Done():
				return
			}
		}

		attemptCtx, cancel := context.WithTimeout(reqCtx, progressRequestTimeout)
		retry := p.send(attemptCtx, body, content)
		cancel()

		if !retry {
			return
		}
	}
}

// send возвращает true, если запрос имеет смысл повторить.
func (p *progressSender) send(ctx context.Context, body []byte, content string) (retry bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		log.Printf("opencode: cannot build saveSteps request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if p.secret != "" {
		req.Header.Set("X-Runner-Signature", podpis(p.token, content, p.secret))
	}

	resp, err := p.client.Do(req)
	if err != nil {
		log.Printf("opencode: saveSteps request failed: %v", err)
		return true
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false
	}

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	log.Printf("opencode: saveSteps failed: status=%d body=%s", resp.StatusCode, obrezatTekst(string(data)))

	return resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests
}

// podpis — hex(HMAC-SHA256(token + stepsContent, secret)), нижний регистр.
func podpis(token, content, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(token))
	mac.Write([]byte(content))
	return hex.EncodeToString(mac.Sum(nil))
}

// obrezatTekst приводит текст шага к валидному UTF-8 и обрезает хвост.
// Перед вызовом текст уже должен быть очищен от API-ключа.
func obrezatTekst(s string) string {
	s = strings.ToValidUTF8(s, "?")
	if len(s) > stepResponseLimit {
		s = s[len(s)-stepResponseLimit:]
		s = strings.ToValidUTF8(s, "?")
	}
	return s
}
