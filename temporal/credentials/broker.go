// Package credentials получает just-in-time git-креденшелы раннера у
// backend-а. Токен живёт только в памяти на время одной git-операции и
// никогда не сериализуется в Temporal payload, state или логи.
package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"runner/config"
)

const (
	// Path — внутренний runner-facing endpoint Unitman.
	Path = "/api/internal/repo/credentials"

	// DefaultTimeout — короткий запрос: broker не должен задерживать git.
	DefaultTimeout = 10 * time.Second

	maxResponseBytes = 64 * 1024
)

// Grant — короткоживущий доступ к одному репозиторию.
type Grant struct {
	RepoID    string `json:"repoId"`
	ProjectID string `json:"projectId"`
	RepoURL   string `json:"repoUrl"`
	Username  string `json:"username"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

// Client — HTTP-клиент broker-а, подписывающий запрос per-runner секретом.
type Client struct {
	BaseURL    string
	RunnerID   string
	Secret     string
	HTTPClient *http.Client
}

// NewClient создаёт клиент broker-а.
func NewClient(baseURL, runnerID, secret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}

	return &Client{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		RunnerID:   strings.TrimSpace(runnerID),
		Secret:     secret,
		HTTPClient: httpClient,
	}
}

// FromConfig собирает клиент из config.json и RUNNER_BACKEND_URL override.
// Секрет раннера в env не берётся: только из config.json.
func FromConfig(configuration config.Configuration) *Client {
	baseURL := strings.TrimSpace(os.Getenv("RUNNER_BACKEND_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(configuration.ApiHost)
	}

	return NewClient(baseURL, configuration.RunnerID, configuration.RunnerSecret, nil)
}

// FetchFromConfig — удобная обёртка для активностей: читает config.json и
// запрашивает грант. Ошибки не содержат токен/URL.
func FetchFromConfig(ctx context.Context, repoID, projectID, unitID string) (Grant, error) {
	configuration, err := config.ParseConfig()
	if err != nil {
		return Grant{}, fmt.Errorf("credentials: read config.json: %w", err)
	}

	return FromConfig(configuration).Fetch(ctx, repoID, projectID, unitID)
}

type requestPayload struct {
	RepoID    string `json:"repoId"`
	ProjectID string `json:"projectId"`
	UnitID    string `json:"unitId,omitempty"`
}

// Fetch запрашивает грант для repo/project/unit.
//
// Fail closed: любая ошибка broker-а, невалидный ответ, userinfo в repoUrl
// или истёкший TTL возвращаются как ошибка без fallback на URL с токеном.
func (c *Client) Fetch(ctx context.Context, repoID, projectID, unitID string) (Grant, error) {
	if c == nil {
		return Grant{}, errors.New("credentials: broker client is nil")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return Grant{}, errors.New("credentials: backend URL is empty")
	}
	if strings.TrimSpace(c.RunnerID) == "" || strings.TrimSpace(c.Secret) == "" {
		return Grant{}, errors.New("credentials: runner identity is not configured")
	}
	if strings.TrimSpace(repoID) == "" || strings.TrimSpace(projectID) == "" {
		return Grant{}, errors.New("credentials: repoId/projectId are required")
	}

	body, err := json.Marshal(requestPayload{
		RepoID:    strings.TrimSpace(repoID),
		ProjectID: strings.TrimSpace(projectID),
		UnitID:    strings.TrimSpace(unitID),
	})
	if err != nil {
		return Grant{}, fmt.Errorf("credentials: marshal request: %w", err)
	}

	requestURL := c.BaseURL + Path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return Grant{}, fmt.Errorf("credentials: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(config.RunnerIDHeader, c.RunnerID)
	req.Header.Set(config.RunnerSignatureHeader, config.Sign(c.Secret, body))

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return Grant{}, fmt.Errorf("credentials: POST %s: %w", Path, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return Grant{}, fmt.Errorf("credentials: broker returned status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Grant{}, fmt.Errorf("credentials: read response: %w", err)
	}

	var grant Grant
	if err := json.Unmarshal(raw, &grant); err != nil {
		return Grant{}, fmt.Errorf("credentials: decode response: %w", err)
	}

	if err := grant.validate(time.Now()); err != nil {
		return Grant{}, err
	}

	return grant, nil
}

func (g Grant) validate(now time.Time) error {
	if strings.TrimSpace(g.RepoURL) == "" {
		return errors.New("credentials: broker returned empty repoUrl")
	}

	parsed, err := url.Parse(g.RepoURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("credentials: broker returned invalid repoUrl")
	}
	if parsed.User != nil {
		return errors.New("credentials: broker returned repoUrl with userinfo")
	}

	if strings.TrimSpace(g.Username) == "" || strings.TrimSpace(g.Token) == "" {
		return errors.New("credentials: broker returned incomplete grant")
	}

	if g.ExpiresAt <= now.Unix() {
		return errors.New("credentials: grant is expired")
	}

	return nil
}
