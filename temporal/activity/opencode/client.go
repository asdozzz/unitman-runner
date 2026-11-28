package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// OpenCodeExecutor — шов между активностью и конкретным клиентом OpenCode.
// Благодаря ему активность можно тестировать без сети, а протокол общения с
// OpenCode — менять независимо от Temporal-контракта.
type OpenCodeExecutor interface {
	Execute(ctx context.Context, req OpenCodeRequest) (OpenCodeAnswer, error)
}

// OpenCodeRequest — один запрос в OpenCode.
//
// WorkDir — рабочий каталог хода (cwd), Roots — дополнительные корни
// workspace (multi-root). HTTP-клиент передаёт их в системном сообщении,
// чтобы модель работала только внутри разрешённых каталогов юнитов.
type OpenCodeRequest struct {
	SessionID string
	URL       string
	Model     string
	APIKey    string
	WorkDir   string
	Roots     []string
	Messages  []Replika
	// AgentName/AgentPrompt задают выбранный мод агента. Пустое имя
	// означает legacy-ход без `--agent`.
	AgentName   string
	AgentPrompt string
	// AgentDenyEdits запрещает правку файлов для plan-подобного мода.
	AgentDenyEdits bool
	// Workspace переключает CLI executor на каталог /workspace.
	Workspace bool
	// ReadOnly запрещает ход писать в git (permission bash git
	// commit/push=deny и инструкция в промпте). Включается у любого
	// read-only хода: и по юниту, и по workspace.
	ReadOnly bool
	// DenyEdits дополнительно запрещает правку файлов (permission
	// edit=deny и инструкция в промпте). Устанавливается только у
	// read-only хода по юниту: правки файлов разрешены исключительно
	// при включённом редактировании (15.7-а). У read-only workspace
	// правки в рабочей копии разрешены — запрещена запись в git.
	DenyEdits bool
	// ArtifactDir — контейнерный каталог-исходящий ящик, куда CLI-агент
	// складывает итоговые документы хода. Заполняется только у workspace-хода;
	// HTTPExecutor поле не читает.
	ArtifactDir string
}

// OpenCodeAnswer — нормализованный ответ OpenCode.
type OpenCodeAnswer struct {
	SessionID *string
	Text      string
	Questions []Vopros
}

// ErrUnexpectedResponse означает, что OpenCode ответил, но ответ не удалось
// разобрать. Активность превращает такую ошибку в
// opencode.turn.unexpected_kind.
var ErrUnexpectedResponse = errors.New("opencode: unexpected response")

const (
	clientBudget      = 13 * time.Minute
	clientAttempts    = 3
	clientRetryDelay  = 300 * time.Millisecond
	maxResponseBytes  = 32 << 20
	maxErrorBodyBytes = 4 << 10
)

// HTTPExecutor — клиент OpenAI-совместимого chat-completions endpoint'а.
// URL из payload'а считается базой API: /chat/completions добавляется, если
// его там ещё нет. Ключ передаётся только в заголовке Authorization.
type HTTPExecutor struct {
	Client *http.Client
}

func NewHTTPExecutor(c *http.Client) *HTTPExecutor {
	if c == nil {
		c = &http.Client{}
	}
	return &HTTPExecutor{Client: c}
}

// NewClient — короткий алиас для удобства wiring'а.
func NewClient(c *http.Client) *HTTPExecutor {
	return NewHTTPExecutor(c)
}

// Execute выполняет один ход. Собственный дедлайн хода держится заметно ниже
// 15 минут, которые живёт workflow.
func (e *HTTPExecutor) Execute(ctx context.Context, req OpenCodeRequest) (OpenCodeAnswer, error) {
	if strings.TrimSpace(req.URL) == "" {
		return OpenCodeAnswer{}, errors.New("opencode: URL is empty")
	}
	if strings.TrimSpace(req.Model) == "" {
		return OpenCodeAnswer{}, errors.New("opencode: model is empty")
	}
	if strings.TrimSpace(req.APIKey) == "" {
		return OpenCodeAnswer{}, errors.New("opencode: api key is empty")
	}

	runCtx, cancel := context.WithTimeout(ctx, clientBudget)
	defer cancel()

	payload := chatRequest{
		Model:      req.Model,
		Messages:   buildChatMessagesWithPrompt(systemPromptFor(req.WorkDir, req.Roots), req.Messages),
		Tools:      []tool{askUserTool()},
		ToolChoice: "auto",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return OpenCodeAnswer{}, fmt.Errorf("opencode: cannot encode request: %w", err)
	}

	endpoint := chatCompletionsURL(req.URL)

	var lastErr error
	for attempt := 0; attempt < clientAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * clientRetryDelay
			select {
			case <-time.After(delay):
			case <-runCtx.Done():
				if ctx.Err() != nil {
					return OpenCodeAnswer{}, ctx.Err()
				}
				return OpenCodeAnswer{}, runCtx.Err()
			}
		}

		resp, err := e.do(runCtx, endpoint, req.APIKey, body)
		if err != nil {
			if runCtx.Err() != nil {
				if ctx.Err() != nil {
					return OpenCodeAnswer{}, ctx.Err()
				}
				return OpenCodeAnswer{}, runCtx.Err()
			}
			lastErr = err
			continue
		}

		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("opencode: cannot read response: %w", readErr)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.Printf("opencode: request failed: status=%d body=%s", resp.StatusCode, responseBodyForLog(responseBody, req.APIKey))

			if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests {
				lastErr = fmt.Errorf("opencode: request failed with status %d", resp.StatusCode)
				continue
			}

			return OpenCodeAnswer{}, fmt.Errorf("opencode: request failed with status %d", resp.StatusCode)
		}

		answer, parseErr := parseChatResponse(responseBody, req.APIKey)
		if parseErr != nil {
			return OpenCodeAnswer{}, parseErr
		}
		if answer.SessionID == nil && strings.TrimSpace(req.SessionID) != "" {
			session := req.SessionID
			answer.SessionID = &session
		}
		return answer, nil
	}

	if lastErr == nil {
		lastErr = errors.New("opencode: request failed")
	}
	return OpenCodeAnswer{}, fmt.Errorf("opencode: request failed: %w", lastErr)
}

func (e *HTTPExecutor) do(ctx context.Context, endpoint, apiKey string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("opencode: cannot build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := e.Client
	if client == nil {
		client = &http.Client{}
	}

	resp, err := client.Do(req)
	if err != nil {
		// url.Error содержит URL, но не заголовки и не ключ.
		return nil, fmt.Errorf("opencode: request failed: %w", err)
	}
	return resp, nil
}

// chatCompletionsURL принимает и полный endpoint, и базу API.
func chatCompletionsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

// ─── запрос ──────────────────────────────────────────────────────────────────

const systemPrompt = `Ты — OpenCode, ассистент внутри Unitman. Выполни запрос пользователя и дай полезный ответ на русском языке.
Если для продолжения действительно нужен выбор или уточнение пользователя, не угадывай, а вызови инструмент ask_user.
В ask_user передай один или несколько вопросов. У каждого вопроса должен быть текст и, при необходимости, варианты выбора.
Если вопрос допускает свободный ответ, установи allowFreeText = true.`

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type tool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []chatMessage `json:"messages"`
	Tools      []tool        `json:"tools,omitempty"`
	ToolChoice string        `json:"tool_choice,omitempty"`
}

func buildChatMessages(messages []Replika) []chatMessage {
	return buildChatMessagesWithPrompt(systemPrompt, messages)
}

func buildChatMessagesWithPrompt(prompt string, messages []Replika) []chatMessage {
	out := make([]chatMessage, 0, len(messages)+1)
	out = append(out, chatMessage{Role: "system", Content: prompt})

	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "assistant" && role != "system" {
			continue
		}
		if strings.TrimSpace(message.Text) == "" {
			continue
		}
		out = append(out, chatMessage{Role: role, Content: message.Text})
	}

	return out
}

// systemPromptFor дополняет системный промпт рамками workspace: модель должна
// работать только в каталогах привязанных к сессии юнитов.
func systemPromptFor(workDir string, roots []string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return systemPrompt
	}

	var builder strings.Builder
	builder.WriteString(systemPrompt)
	builder.WriteString("\n\nРабочий каталог хода: ")
	builder.WriteString(workDir)
	builder.WriteString(".\nРаботай только внутри этого каталога")
	if len(roots) > 0 {
		builder.WriteString(" и перечисленных корней workspace")
	}
	builder.WriteString(". Не читай и не изменяй файлы за их пределами.")

	if len(roots) > 0 {
		builder.WriteString("\nДополнительные корни workspace: ")
		for i, root := range roots {
			if i > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString(root)
		}
		builder.WriteString(".")
	}

	return builder.String()
}

func askUserTool() tool {
	return tool{
		Type: "function",
		Function: toolFunction{
			Name:        "ask_user",
			Description: "Задать пользователю один или несколько вопросов, если без ответа нельзя продолжить.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"questions": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id": map[string]interface{}{
									"type":        "string",
									"description": "Стабильный идентификатор вопроса, например q1.",
								},
								"text": map[string]interface{}{
									"type":        "string",
									"description": "Текст вопроса для пользователя.",
								},
								"allowFreeText": map[string]interface{}{
									"type":        "boolean",
									"description": "true, если пользователь может ответить свободным текстом.",
								},
								"options": map[string]interface{}{
									"type": "array",
									"items": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"id": map[string]interface{}{
												"type": "string",
											},
											"label": map[string]interface{}{
												"type": "string",
											},
											"description": map[string]interface{}{
												"type": "string",
											},
										},
										"required": []string{"id", "label"},
									},
								},
							},
							"required": []string{"text"},
						},
					},
				},
				"required": []string{"questions"},
			},
		},
	}
}

// ─── ответ ───────────────────────────────────────────────────────────────────

type chatResponse struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	SessionID2 string `json:"sessionId"`
	Choices    []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type wireOption struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Description *string `json:"description"`
}

type wireQuestion struct {
	ID            string       `json:"id"`
	Text          string       `json:"text"`
	AllowFreeText bool         `json:"allowFreeText"`
	Options       []wireOption `json:"options"`
}

type wireAsk struct {
	Questions []wireQuestion `json:"questions"`
}

type wireStructuredContent struct {
	Questions []wireQuestion `json:"questions"`
	Answer    *string        `json:"answer"`
	Text      *string        `json:"text"`
}

func parseChatResponse(body []byte, apiKey string) (OpenCodeAnswer, error) {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return OpenCodeAnswer{}, fmt.Errorf("%w: invalid JSON: %v", ErrUnexpectedResponse, err)
	}

	if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
		return OpenCodeAnswer{}, fmt.Errorf("opencode: %s", redact(response.Error.Message, apiKey))
	}
	if len(response.Choices) == 0 {
		return OpenCodeAnswer{}, fmt.Errorf("%w: response has no choices", ErrUnexpectedResponse)
	}

	message := response.Choices[0].Message
	text := parseContent(message.Content)
	questions := make([]Vopros, 0)

	for _, call := range message.ToolCalls {
		name := strings.ToLower(strings.TrimSpace(call.Function.Name))
		if name != "ask_user" && name != "ask_questions" && name != "ask" {
			continue
		}

		var ask wireAsk
		if err := json.Unmarshal([]byte(call.Function.Arguments), &ask); err != nil {
			log.Printf("opencode: cannot parse ask_user arguments: %v", err)
			continue
		}
		questions = append(questions, wireQuestionsToVopros(ask.Questions)...)
	}

	if structured, structuredAnswer := parseStructuredContent(text); len(structured) > 0 || structuredAnswer != "" {
		questions = append(questions, structured...)
		if structuredAnswer != "" {
			text = structuredAnswer
		}
	}

	sessionID := strings.TrimSpace(response.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(response.SessionID2)
	}
	if sessionID != "" {
		session := sessionID
		return OpenCodeAnswer{SessionID: &session, Text: text, Questions: questions}, nil
	}

	return OpenCodeAnswer{Text: text, Questions: questions}, nil
}

func parseContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			if part.Text != "" {
				builder.WriteString(part.Text)
			}
		}
		return builder.String()
	}

	var object struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		if object.Text != "" {
			return object.Text
		}
		return object.Content
	}

	return ""
}

// parseStructuredContent поддерживает случай, когда модель вместо tool call
// вернула JSON-объект с вопросами или ответом.
func parseStructuredContent(text string) ([]Vopros, string) {
	trimmed := stripCodeFence(strings.TrimSpace(text))
	if !strings.HasPrefix(trimmed, "{") {
		return nil, ""
	}

	var content wireStructuredContent
	if err := json.Unmarshal([]byte(trimmed), &content); err != nil {
		return nil, ""
	}

	questions := wireQuestionsToVopros(content.Questions)
	switch {
	case content.Answer != nil && strings.TrimSpace(*content.Answer) != "":
		return questions, *content.Answer
	case content.Text != nil && strings.TrimSpace(*content.Text) != "":
		return questions, *content.Text
	default:
		return questions, ""
	}
}

func stripCodeFence(text string) string {
	if !strings.HasPrefix(text, "```") {
		return text
	}

	text = strings.TrimPrefix(text, "```")
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[index+1:]
	}
	text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	return strings.TrimSpace(text)
}

func wireQuestionsToVopros(in []wireQuestion) []Vopros {
	out := make([]Vopros, 0, len(in))
	for _, question := range in {
		vopros := Vopros{
			ID:            question.ID,
			Text:          question.Text,
			AllowFreeText: question.AllowFreeText,
		}

		if question.Options != nil {
			vopros.Options = make([]Variant, 0, len(question.Options))
			for _, option := range question.Options {
				vopros.Options = append(vopros.Options, Variant{
					ID:          option.ID,
					Label:       option.Label,
					Description: option.Description,
				})
			}
		} else {
			vopros.Options = []Variant{}
		}

		// Вопрос без вариантов обязан принимать свободный ответ, иначе
		// пользователь физически не сможет на него ответить.
		if len(vopros.Options) == 0 {
			vopros.AllowFreeText = true
		}

		out = append(out, vopros)
	}
	return out
}

func responseBodyForLog(body []byte, apiKey string) string {
	// Сначала вырезаем секрет из всего тела, потом обрезаем хвост: иначе ключ
	// мог бы попасть в лог частично на границе обрезки.
	text := redact(string(body), apiKey)
	if len(text) > maxErrorBodyBytes {
		text = text[len(text)-maxErrorBodyBytes:]
	}
	return obrezatTekst(text)
}
