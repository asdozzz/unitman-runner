package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"runner/config"
)

// Контракт build-state описан в plans/runner-multi-runner.md §3:
//
//	GET /api/runner/{runnerId}/projects/{projectId}/build-state
//	PUT /api/runner/{runnerId}/projects/{projectId}/build-state
//
// Callback-подпись та же, что у остальных callback-ов раннера:
// X-Runner-Signature = hex(HMAC-SHA256(raw_body, RunnerSecret)).
// Для GET подписывается пустой payload. Секрет берётся из config.json.
const (
	// ProjectBuildStateSignatureHeader — заголовок HMAC-подписи build-state.
	ProjectBuildStateSignatureHeader = config.RunnerSignatureHeader
	// ProjectBuildStateRunnerIDHeader — заголовок идентификатора раннера.
	ProjectBuildStateRunnerIDHeader = config.RunnerIDHeader
	// ProjectBuildStateDefaultTimeout — таймаут HTTP-запроса build-state.
	ProjectBuildStateDefaultTimeout = 10 * time.Second

	// Коды ошибок backend-а из контракта §3.
	ProjectBuildStateInvalidProjectIDCode = "project_build_state.invalid_project_id"
	ProjectBuildStateInvalidPayloadCode   = "project_build_state.invalid_payload"
	RunnerNotFoundCode                    = "runner.not_found"
	RunnerInactiveCode                    = "runner.inactive"
	RunnerInvalidSignatureCode            = "runner.invalid_signature"
	RunnerAuthDisabledCode                = "runner.auth_disabled"
)

// ProjectBuildState — ответ backend-а (GET/PUT): собран ли проект на раннере.
type ProjectBuildState struct {
	Built   bool    `json:"built"`
	BuiltAt *int64  `json:"builtAt"`
	Commit  *string `json:"commit"`
	Branch  *string `json:"branch"`
}

// ProjectBuildRecord — тело PUT build-state. Все метаданные опциональны,
// builtAt по умолчанию выставляет backend, но раннер передаёт своё время.
type ProjectBuildRecord struct {
	BuiltAt *int64  `json:"builtAt,omitempty"`
	Commit  *string `json:"commit,omitempty"`
	Branch  *string `json:"branch,omitempty"`
}

// ProjectBuildStateAPIError — типизированная ошибка build-state API.
//
// Code содержит машинный код backend-а (например, runner.inactive), StatusCode —
// HTTP-статус. Ошибки runner.not_found и runner.inactive различимы через
// методы IsRunnerNotFound/IsRunnerInactive.
type ProjectBuildStateAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

// Error реализует error.
func (e *ProjectBuildStateAPIError) Error() string {
	if e == nil {
		return "build-state: неизвестная ошибка backend"
	}

	detail := e.Message
	if detail == "" {
		detail = e.Code
	}
	if detail == "" {
		detail = http.StatusText(e.StatusCode)
	}

	if e.Code != "" && e.Code != detail {
		return fmt.Sprintf("build-state: backend вернул HTTP %d (%s): %s", e.StatusCode, e.Code, detail)
	}

	return fmt.Sprintf("build-state: backend вернул HTTP %d: %s", e.StatusCode, detail)
}

// IsRunnerNotFound сообщает, что backend не знает runnerId (404).
func (e *ProjectBuildStateAPIError) IsRunnerNotFound() bool {
	return e != nil && (e.Code == RunnerNotFoundCode || (e.StatusCode == http.StatusNotFound && e.Code == ""))
}

// IsRunnerInactive сообщает, что раннер существует, но неактивен (409).
func (e *ProjectBuildStateAPIError) IsRunnerInactive() bool {
	return e != nil && (e.Code == RunnerInactiveCode || (e.StatusCode == http.StatusConflict && e.Code == ""))
}

// IsInvalidSignature сообщает, что подпись отсутствует или неверна (401).
func (e *ProjectBuildStateAPIError) IsInvalidSignature() bool {
	return e != nil && (e.Code == RunnerInvalidSignatureCode || (e.StatusCode == http.StatusUnauthorized && e.Code == ""))
}

// IsAuthDisabled сообщает, что callback-аутентификация выключена на backend (503).
func (e *ProjectBuildStateAPIError) IsAuthDisabled() bool {
	return e != nil && (e.Code == RunnerAuthDisabledCode || (e.StatusCode == http.StatusServiceUnavailable && e.Code == ""))
}

// AsProjectBuildStateAPIError извлекает типизированную ошибку из цепочки.
func AsProjectBuildStateAPIError(err error) (*ProjectBuildStateAPIError, bool) {
	var apiErr *ProjectBuildStateAPIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}

	return nil, false
}

// ProjectBuildStateClient — минимальный клиент build-state API.
//
// BaseURL, RunnerID, Secret и HTTPClient инъектируемы: в проде они берутся из
// config.json, в тестах — из httptest.
type ProjectBuildStateClient struct {
	BaseURL    string
	RunnerID   string
	Secret     string
	HTTPClient *http.Client
}

// NewProjectBuildStateClient создаёт клиент с дефолтным таймаутом.
func NewProjectBuildStateClient(baseURL, runnerID, secret string, httpClient *http.Client) *ProjectBuildStateClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: ProjectBuildStateDefaultTimeout}
	}

	return &ProjectBuildStateClient{
		BaseURL:    baseURL,
		RunnerID:   runnerID,
		Secret:     secret,
		HTTPClient: httpClient,
	}
}

// GetBuildState проверяет, собран ли проект на этом раннере.
func (c *ProjectBuildStateClient) GetBuildState(ctx context.Context, projectID string) (ProjectBuildState, error) {
	var state ProjectBuildState
	err := c.do(ctx, http.MethodGet, projectID, nil, &state)

	return state, err
}

// RecordBuildState идемпотентно записывает факт сборки (upsert по
// runner_id/project_id).
func (c *ProjectBuildStateClient) RecordBuildState(ctx context.Context, projectID string, record ProjectBuildRecord) (ProjectBuildState, error) {
	var state ProjectBuildState

	rawBody, err := json.Marshal(record)
	if err != nil {
		return state, fmt.Errorf("build-state: marshal record: %w", err)
	}

	err = c.do(ctx, http.MethodPut, projectID, rawBody, &state)

	return state, err
}

// do выполняет подписанный запрос и декодирует успешный ответ.
func (c *ProjectBuildStateClient) do(ctx context.Context, method, projectID string, rawBody []byte, out any) error {
	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		return errors.New("build-state: не задан базовый URL backend (ApiHost)")
	}
	if strings.TrimSpace(c.RunnerID) == "" {
		return errors.New("build-state: не задан runnerId")
	}
	if strings.TrimSpace(projectID) == "" {
		return errors.New("build-state: не задан projectId")
	}
	if strings.TrimSpace(c.Secret) == "" {
		return errors.New("build-state: RunnerSecret не задан в config.json")
	}

	// GET подписывает пустое тело; PUT — ровно те байты, что уходят в запрос.
	if rawBody == nil {
		rawBody = []byte{}
	}

	endpoint := strings.TrimRight(baseURL, "/") +
		"/api/runner/" + url.PathEscape(c.RunnerID) +
		"/projects/" + url.PathEscape(projectID) +
		"/build-state"

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return fmt.Errorf("build-state: create %s request: %w", method, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ProjectBuildStateRunnerIDHeader, c.RunnerID)
	req.Header.Set(ProjectBuildStateSignatureHeader, signProjectBuildState(rawBody, c.Secret))

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: ProjectBuildStateDefaultTimeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("build-state: %s %s: %w", method, endpoint, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("build-state: read %s response: %w", method, err)
	}

	if resp.StatusCode != http.StatusOK {
		return newProjectBuildStateAPIError(resp.StatusCode, body)
	}

	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("build-state: decode %s response: %w", method, err)
		}
	}

	return nil
}

// signProjectBuildState считает hex(HMAC-SHA256(raw_body, secret)).
func signProjectBuildState(rawBody []byte, secret string) string {
	return config.Sign(secret, rawBody)
}

// newProjectBuildStateAPIError разбирает тело ошибки backend-а.
//
// Формат приложения — FailResponse: {"status":"fail","data":{"code":...,"message":...}}.
// На всякий случай поддерживаются и плоские поля code/message/error/errorCode.
func newProjectBuildStateAPIError(status int, body []byte) error {
	code, message := projectBuildStateErrorFields(body)
	if code == "" {
		code = fallbackBuildStateErrorCode(status)
	}

	if message == "" {
		message = strings.TrimSpace(string(body))
	}

	return &ProjectBuildStateAPIError{
		StatusCode: status,
		Code:       code,
		Message:    message,
	}
}

// projectBuildStateErrorFields извлекает код и сообщение из тела ошибки.
func projectBuildStateErrorFields(body []byte) (string, string) {
	var payload struct {
		Status    string `json:"status"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		Error     string `json:"error"`
		ErrorCode string `json:"errorCode"`
		Data      struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Error   string `json:"error"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}

	code := firstNonEmpty(payload.Data.Code, payload.Code, payload.ErrorCode, payload.Error, payload.Data.Error)
	message := firstNonEmpty(payload.Data.Message, payload.Message, payload.Data.Error, payload.Error)

	return code, message
}

// fallbackBuildStateErrorCode подставляет код по HTTP-статусу, если backend не
// прислал машинный код. Это сохраняет различимость runner.not_found/inactive.
func fallbackBuildStateErrorCode(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return RunnerInvalidSignatureCode
	case http.StatusNotFound:
		return RunnerNotFoundCode
	case http.StatusConflict:
		return RunnerInactiveCode
	case http.StatusServiceUnavailable:
		return RunnerAuthDisabledCode
	default:
		return ""
	}
}

// firstNonEmpty возвращает первое непустое значение.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}
