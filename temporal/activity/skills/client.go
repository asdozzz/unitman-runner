// Package skills загружает эффективный набор OpenCode-навыков (skills) с
// backend-а и раскладывает их в staging-каталог юнита/workspace.
//
// Пакет намеренно автономен и не зависит от пакетов unit/workspace, чтобы
// активности могли вызывать его без циклов импорта. Любая ошибка сети или
// записи здесь не является фатальной для подготовки юнита или хода агента:
// вызывающая сторона логирует её и продолжает работу.
package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"runner/config"
)

const (
	// ResolvePath — внутренний endpoint backend-а, отдающий эффективные навыки.
	ResolvePath = "/api/internal/opencode/skills/resolve"
	// FormatVersion — версия контракта запроса.
	FormatVersion = 1
	// MaxTotalBytes — запрашиваемый лимит суммарного размера навыков.
	MaxTotalBytes = 524288

	defaultResolveTimeout = 10 * time.Second
	maxResolveBodyBytes   = 4 << 20

	// Внутренние лимиты staging-а (совпадают с запрошенным лимитом).
	maxSkillContentBytes = 32 << 10
	maxTotalContentBytes = 512 << 10
)

// Skill — один навык из ответа backend-а.
//
// Version объявлен как json.Number, потому что backend отдаёт версию числом
// (`"version": 3`), а в манифесте она участвует строкой. json.Number
// принимает и число, и строку, поэтому смена представления на любой стороне
// не ломает декодирование.
type Skill struct {
	SkillID       string      `json:"skillId"`
	Slug          string      `json:"slug"`
	ContainerName string      `json:"containerName"`
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	Content       string      `json:"content"`
	Version       json.Number `json:"version"`
	Hash          string      `json:"hash"`
	Scope         string      `json:"scope"`
	ProjectID     string      `json:"projectId"`
}

// ResolveResult — результат разрешения навыков.
type ResolveResult struct {
	Skills       []Skill
	ManifestHash string
	TotalBytes   int
	Truncated    bool
	// NotDeployed означает, что endpoint вернул 404 (фича не развёрнута).
	// В этом случае раннер деградирует до пустого набора навыков.
	NotDeployed bool
}

// Client — подписанный HTTP-клиент resolve-запроса.
type Client struct {
	BaseURL    string
	RunnerID   string
	Secret     string
	HTTPClient *http.Client
}

// NewClientFromConfig собирает клиент из config.json, переиспользуя общие
// поля ApiHost/RunnerId/RunnerSecret и хелпер config.Sign.
func NewClientFromConfig() (*Client, error) {
	configuration, err := config.ParseConfig()
	if err != nil {
		return nil, fmt.Errorf("skills: read config.json: %w", err)
	}

	return &Client{
		BaseURL:  configuration.ApiHost,
		RunnerID: configuration.RunnerID,
		Secret:   configuration.RunnerSecret,
	}, nil
}

type resolveRequest struct {
	ProjectIDs    []string `json:"projectIds"`
	FormatVersion int      `json:"formatVersion"`
	MaxTotalBytes int      `json:"maxTotalBytes"`
}

type resolveEnvelope struct {
	Status  string       `json:"status"`
	Data    *resolveData `json:"data"`
	Message string       `json:"message"`
	Code    string       `json:"code"`
}

type resolveData struct {
	Skills       []Skill `json:"skills"`
	ManifestHash string  `json:"manifestHash"`
	TotalBytes   int     `json:"totalBytes"`
	Truncated    bool    `json:"truncated"`
}

// Resolve выполняет подписанный POST resolve-запроса. Тело сериализуется один
// раз, подписывается ровно тем же срезом байт и отправляется без изменений.
//
// 404 трактуется как «фича не развёрнута»: возвращается пустой результат без
// ошибки, чтобы старый backend не ломал подготовку.
func (c *Client) Resolve(ctx context.Context, projectIDs []string) (ResolveResult, error) {
	rawBody, err := json.Marshal(resolveRequest{
		ProjectIDs:    normalizeProjectIDs(projectIDs),
		FormatVersion: FormatVersion,
		MaxTotalBytes: MaxTotalBytes,
	})
	if err != nil {
		return ResolveResult{}, fmt.Errorf("skills: marshal resolve request: %w", err)
	}

	url := joinURL(c.BaseURL, ResolvePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		return ResolveResult{}, fmt.Errorf("skills: build resolve request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(config.RunnerIDHeader, c.RunnerID)
	req.Header.Set(config.RunnerSignatureHeader, config.Sign(c.Secret, rawBody))

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultResolveTimeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// url.Error не содержит секретов; тело и подпись не логируем.
		return ResolveResult{}, fmt.Errorf("skills: resolve request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		log.Printf("skills: resolve endpoint not deployed (404), degrading to no skills")
		return ResolveResult{NotDeployed: true, ManifestHash: ManifestHash(nil)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResolveBodyBytes))
	if err != nil {
		return ResolveResult{}, fmt.Errorf("skills: read resolve response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		code, message := decodeErrorEnvelope(body)
		log.Printf("skills: resolve failed: status=%d code=%q message=%q", resp.StatusCode, code, truncateForLog(message))
		return ResolveResult{}, fmt.Errorf("skills: resolve failed with status %d", resp.StatusCode)
	}

	var envelope resolveEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ResolveResult{}, fmt.Errorf("skills: decode resolve response: %w", err)
	}
	if envelope.Status != "success" || envelope.Data == nil {
		log.Printf("skills: resolve returned non-success status=%q code=%q message=%q", envelope.Status, envelope.Code, truncateForLog(envelope.Message))
		return ResolveResult{}, fmt.Errorf("skills: resolve returned status %q", envelope.Status)
	}

	return ResolveResult{
		Skills:       envelope.Data.Skills,
		ManifestHash: envelope.Data.ManifestHash,
		TotalBytes:   envelope.Data.TotalBytes,
		Truncated:    envelope.Data.Truncated,
	}, nil
}

// ManifestHash воспроизводит контракт backend-а:
//
//	sha256("\n".join(sorted("<containerName>:<version>:<hash>")))
//
// Строки не завершаются переводом строки; пустой набор даёт sha256("").
func ManifestHash(skills []Skill) string {
	lines := make([]string, 0, len(skills))
	for _, skill := range skills {
		lines = append(lines, skill.ContainerName+":"+string(skill.Version)+":"+skill.Hash)
	}
	sort.Strings(lines)

	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))

	return hex.EncodeToString(sum[:])
}

func normalizeProjectIDs(projectIDs []string) []string {
	seen := make(map[string]struct{}, len(projectIDs))
	result := make([]string, 0, len(projectIDs))
	for _, id := range projectIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	// Детерминированный порядок запроса: backend не обязан сохранять порядок,
	// но стабильный запрос проще кэшировать и отлаживать.
	sort.Strings(result)

	return result
}

func decodeErrorEnvelope(body []byte) (string, string) {
	var envelope resolveEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", ""
	}

	return envelope.Code, envelope.Message
}

func truncateForLog(value string) string {
	const limit = 256
	if len(value) <= limit {
		return value
	}

	return value[:limit] + "…"
}

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
