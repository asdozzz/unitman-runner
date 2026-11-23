// Package monitor реализует фоновые циклы раннера: health-push,
// сверку фактического состояния контейнеров и их очистку.
package monitor

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"runner/threat"
)

const (
	healthPath        = "/api/runner/health"
	notificationsPath = "/api/runner/unitStateNotifications"

	defaultHTTPTimeout = 10 * time.Second
)

// Client отправляет данные раннера на backend unitman.
type Client struct {
	BaseURL    string
	Secret     string
	RunnerID   string
	TaskQueue  string
	HTTPClient *http.Client
}

// HealthPayload — тело health-push запроса.
type HealthPayload struct {
	RunnerID    string `json:"runnerId"`
	TaskQueue   string `json:"taskQueue"`
	Active      bool   `json:"active"`
	DockerStats string `json:"dockerStats"`
	MemInfo     string `json:"memInfo"`
	Unixtime    int64  `json:"unixtime"`
	// Resources — структурированный снимок ресурсов сервера (CPU, память,
	// диски, Docker) с уровнем угрозы. Старые backend'ы поле игнорируют.
	Resources *threat.Snapshot `json:"resources,omitempty"`
}

// Notification — уведомление о расхождении состояния контейнера юнита.
type Notification struct {
	UnitID        string `json:"unitId"`
	ProjectID     string `json:"projectId"`
	UnitName      string `json:"unitName"`
	ProjectName   string `json:"projectName"`
	ExpectedState string `json:"expectedState"`
	ActualState   string `json:"actualState"`
	EventType     string `json:"eventType"`
	DetectedAt    int64  `json:"detectedAt"`
}

// NotificationsPayload — батч уведомлений одного цикла сверки.
type NotificationsPayload struct {
	RunnerID      string         `json:"runnerId"`
	Notifications []Notification `json:"notifications"`
}

// SendHealth отправляет health-push на backend.
func (c *Client) SendHealth(ctx context.Context, payload HealthPayload) error {
	return c.post(ctx, healthPath, payload)
}

// SendNotifications отправляет батч уведомлений о состоянии юнитов.
func (c *Client) SendNotifications(ctx context.Context, payload NotificationsPayload) error {
	return c.post(ctx, notificationsPath, payload)
}

// post сериализует payload, подписывает его и выполняет POST-запрос.
func (c *Client) post(ctx context.Context, path string, payload any) error {
	rawBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("monitor: marshal payload for %s: %w", path, err)
	}

	url := joinURL(c.BaseURL, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		return fmt.Errorf("monitor: create request %s: %w", url, err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.Secret != "" {
		mac := hmac.New(sha256.New, []byte(c.Secret))
		if _, err := mac.Write(rawBody); err != nil {
			return fmt.Errorf("monitor: sign payload for %s: %w", path, err)
		}
		req.Header.Set("X-Runner-Signature", hex.EncodeToString(mac.Sum(nil)))
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("monitor: POST %s: %w", url, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("monitor: POST %s: unexpected status %d", url, resp.StatusCode)
	}

	return nil
}

// joinURL нормализует склейку BaseURL и пути (учитывает trailing slash).
func joinURL(baseURL, path string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if path == "" {
		return trimmed
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return trimmed + path
}
