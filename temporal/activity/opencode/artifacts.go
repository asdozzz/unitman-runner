package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"runner/config"
	"runner/temporal/activity/workspace"
	"runner/temporal/utils"
)

const (
	// artifactDirName — каталог-исходящий ящик артефактов внутри workspace.
	// Не пересекается с unitman_files (read-only input) и каталогами проектов.
	artifactDirName = "unitman_artifacts"

	maxArtifactBytes      = 2 * 1024 * 1024
	maxArtifacts          = 5
	maxTotalArtifactBytes = 8 * 1024 * 1024
	artifactNameMaxLen    = 128

	artifactRequestTimeout = 30 * time.Second
	artifactAttempts       = 3
	artifactRetryDelay     = 300 * time.Millisecond
)

// artifactExtensions — allowlist расширение → MIME. Тело файла считается
// текстовым: бинарные форматы сюда не входят.
var artifactExtensions = map[string]string{
	"md":   "text/markdown",
	"txt":  "text/plain",
	"json": "application/json",
	"yaml": "application/yaml",
	"yml":  "application/yaml",
	"csv":  "text/csv",
	"log":  "text/plain",
}

// artifactDeniedExtensions — явный запрет: даже если формат как-то попал в
// allowlist, эти расширения не принимаются.
var artifactDeniedExtensions = map[string]bool{
	"html": true, "htm": true, "xhtml": true, "svg": true,
	"js": true, "mjs": true, "ts": true,
	"php": true, "sh": true, "py": true,
	"exe": true, "bin": true, "so": true, "dll": true,
	"bat": true, "cmd": true, "ps1": true, "sql": true,
}

// Artifact — метаданные артефакта хода. Содержимого здесь нет никогда.
// JSON-ключи в PascalCase: так их читает бэкенд (HodResult.Attachments).
type Artifact struct {
	FileID      string `json:"FileId"`
	Name        string `json:"Name"`
	Size        int    `json:"Size"`
	ContentType string `json:"ContentType"`
}

// artifactFile — запись каталога-исходящего ящика, полученная из контейнера.
type artifactFile struct {
	Name string
	Size int
}

// validArtifactName пропускает только безопасное имя файла с разрешённым
// расширением. Отклоняет пути, скрытые файлы, NUL/переводы строк, спец-имена
// и превышение длины.
func validArtifactName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\") {
		return false
	}
	if strings.ContainsAny(name, "\x00\r\n") {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	if len(name) > artifactNameMaxLen {
		return false
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return false
	}
	if artifactDeniedExtensions[ext] {
		return false
	}
	_, allowed := artifactExtensions[ext]
	return allowed
}

// artifactContentType возвращает MIME по allowlist расширений.
func artifactContentType(name string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	mime, ok := artifactExtensions[ext]
	if !ok || artifactDeniedExtensions[ext] {
		return "", false
	}
	return mime, true
}

// collectWorkspaceArtifacts читает каталог-исходящий ящик внутри контейнера
// workspace и возвращает отфильтрованный список файлов.
//
// Используется GNU find с -printf '%f\t%s\n': образ workspace построен на
// Debian и содержит GNU findutils. Отсутствие каталога не является ошибкой —
// команда обёрнута в `if [ -d ... ]`. Ошибка возвращается только при сбое
// исполнения команды в контейнере.
func collectWorkspaceArtifacts(
	ctx context.Context,
	workspaceDir, artifactDir string,
	exec func(context.Context, string, string, string, []string) (string, error),
) ([]artifactFile, error) {
	if strings.TrimSpace(artifactDir) == "" {
		return nil, nil
	}
	if exec == nil {
		return nil, fmt.Errorf("artifact: exec function is not configured")
	}

	command := "if [ -d " + shellQuote(artifactDir) + " ]; then " +
		"find " + shellQuote(artifactDir) + " -maxdepth 1 -type f -printf '%f\\t%s\\n'; fi"

	output, err := exec(ctx, workspaceDir, command, workspaceContainerDir, nil)
	if err != nil {
		return nil, err
	}

	files := make([]artifactFile, 0, maxArtifacts)
	total := 0

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}

		name := parts[0]
		size, convErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if convErr != nil {
			continue
		}

		if !validArtifactName(name) {
			continue
		}
		if size <= 0 || size > maxArtifactBytes {
			continue
		}
		if len(files) >= maxArtifacts {
			break
		}
		if total+size > maxTotalArtifactBytes {
			continue
		}

		files = append(files, artifactFile{Name: name, Size: size})
		total += size
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	return files, nil
}

// copyFileFromWorkspace копирует файл из контейнера workspace на хост.
// Переменная для подмены в тестах; имя compose-сервиса совпадает с тем,
// что используют остальные workspace-активности.
var copyFileFromWorkspace = func(ctx context.Context, dir, source, target string) (string, error) {
	args := []string{"docker", "compose", "cp", workspaceComposeService + ":" + source, target}
	return utils.ExecCommandContext(ctx, dir, args)
}

// artifactEndpoint принимает и корень бэкенда, и базу с /api, и полный
// endpoint. Логика повторяет saveStepsURL.
func artifactEndpoint(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}

	switch {
	case strings.HasSuffix(base, "/api/runner/opencode/artifact"):
		return base
	case strings.HasSuffix(base, "/api/runner/opencode"):
		return base + "/artifact"
	case strings.HasSuffix(base, "/api/runner"):
		return base + "/opencode/artifact"
	case strings.HasSuffix(base, "/api"):
		return base + "/runner/opencode/artifact"
	default:
		return base + "/api/runner/opencode/artifact"
	}
}

// artifactURL собирает полный URL загрузки. Имя и contentType кодируются;
// для подписи используется исходное (декодированное) имя.
func artifactURL(base, workflowID, workspaceID, name, contentType string) string {
	endpoint := artifactEndpoint(base)
	if endpoint == "" {
		return ""
	}

	values := url.Values{}
	values.Set("workflowId", workflowID)
	values.Set("workspaceId", workspaceID)
	values.Set("name", name)
	values.Set("contentType", contentType)

	return endpoint + "?" + values.Encode()
}

// artifactSender отправляет артефакты на бэкенд по HMAC-контракту.
type artifactSender struct {
	client     *http.Client
	url        string
	runnerID   string
	secret     string
	workflowID string
}

func newArtifactSender(c *http.Client, base, runnerID, secret, workflowID string) *artifactSender {
	if c == nil {
		c = &http.Client{Timeout: artifactRequestTimeout}
	}

	return &artifactSender{
		client:     c,
		url:        strings.TrimSpace(base),
		runnerID:   runnerID,
		secret:     secret,
		workflowID: workflowID,
	}
}

// artifactSignature — hex(HMAC-SHA256(secret, workflowId+"\n"+name+"\n"+body)).
func artifactSignature(secret, workflowID, name string, body []byte) string {
	macBuf := make([]byte, 0, len(workflowID)+1+len(name)+1+len(body))
	macBuf = append(macBuf, workflowID...)
	macBuf = append(macBuf, '\n')
	macBuf = append(macBuf, name...)
	macBuf = append(macBuf, '\n')
	macBuf = append(macBuf, body...)

	return config.Sign(secret, macBuf)
}

// send загружает один файл. retry=true только для сетевых ошибок и 5xx.
// Пустой workflowID означает «загрузка отключена»: пропуск без ошибки.
func (s *artifactSender) send(ctx context.Context, workspaceID, name, contentType string, body []byte) (Artifact, bool, error) {
	if s == nil {
		return Artifact{}, false, nil
	}
	if strings.TrimSpace(s.workflowID) == "" || strings.TrimSpace(s.url) == "" {
		return Artifact{}, false, nil
	}

	endpoint := artifactURL(s.url, s.workflowID, workspaceID, name, contentType)
	if endpoint == "" {
		return Artifact{}, false, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Artifact{}, false, fmt.Errorf("artifact: cannot build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(config.RunnerIDHeader, s.runnerID)
	req.Header.Set(config.RunnerSignatureHeader, artifactSignature(s.secret, s.workflowID, name, body))

	resp, err := s.client.Do(req)
	if err != nil {
		return Artifact{}, true, fmt.Errorf("artifact: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var parsed struct {
			Status string   `json:"status"`
			Data   Artifact `json:"data"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			return Artifact{}, false, fmt.Errorf("artifact: cannot parse response: %w", err)
		}
		return parsed.Data, false, nil
	}

	retry := resp.StatusCode >= 500
	return Artifact{}, retry, fmt.Errorf("artifact: upload failed with status %d", resp.StatusCode)
}

// sobratIOtpravitArtefakty собирает артефакты хода, загружает их на бэкенд и
// возвращает метаданные. Никогда не возвращает ошибку: сбой артефактов не
// должен менять результат хода.
func (a *Activities) sobratIOtpravitArtefakty(
	ctx context.Context,
	workspaceDir, artifactDir string,
	p *progressSender,
	cmd HodCommand,
) []Artifact {
	if strings.TrimSpace(artifactDir) == "" {
		return nil
	}

	// Артефакты — вспомогательный канал: паника не должна валить ход.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("opencode: panic in artifact collection: %v", r)
		}
	}()

	files, err := collectWorkspaceArtifacts(ctx, workspaceDir, artifactDir, workspace.ExecCommandInWorkspace)
	if err != nil {
		log.Printf("opencode: artifact collect failed: workflowId=%s err=%s", workflowIDFunc(ctx), err)
	}

	sender := newArtifactSender(a.Client, a.BackendURL, a.RunnerID, a.Secret, workflowIDFunc(ctx))

	out := make([]Artifact, 0, len(files))
	for _, file := range files {
		if art, ok := a.uploadArtifact(ctx, workspaceDir, artifactDir, file, sender, p, cmd); ok {
			out = append(out, art)
		}
	}

	// Исходящий ящик больше не нужен: содержимое уже на бэкенде.
	cleanup := "rm -rf " + shellQuote(artifactDir)
	if _, cleanupErr := workspace.ExecCommandInWorkspace(ctx, workspaceDir, cleanup, workspaceContainerDir, nil); cleanupErr != nil {
		log.Printf("opencode: artifact cleanup failed: workflowId=%s err=%s", workflowIDFunc(ctx), cleanupErr)
	}

	return out
}

// uploadArtifact копирует один файл на хост, проверяет содержимое и загружает
// его с ретраями. Возвращает ok=false при любом сбое; ошибка логируется и
// попадает в шаг прогресса, но не наверх.
func (a *Activities) uploadArtifact(
	ctx context.Context,
	workspaceDir, artifactDir string,
	file artifactFile,
	sender *artifactSender,
	p *progressSender,
	cmd HodCommand,
) (Artifact, bool) {
	started := time.Now()

	contentType, ok := artifactContentType(file.Name)
	if !ok {
		p.addStep(ctx, "artifact "+file.Name, "forbidden file type", false, started)
		return Artifact{}, false
	}

	tempFile, err := os.CreateTemp("", "unitman-artifact-*")
	if err != nil {
		log.Printf("opencode: artifact temp file failed: workflowId=%s name=%s err=%s", workflowIDFunc(ctx), file.Name, err)
		p.addStep(ctx, "artifact "+file.Name, err.Error(), false, started)
		return Artifact{}, false
	}
	tempPath := tempFile.Name()
	_ = tempFile.Close()
	defer os.Remove(tempPath)

	source := strings.TrimRight(artifactDir, "/") + "/" + file.Name
	if _, copyErr := copyFileFromWorkspace(ctx, workspaceDir, source, tempPath); copyErr != nil {
		log.Printf("opencode: artifact copy failed: workflowId=%s name=%s size=%d err=%s", workflowIDFunc(ctx), file.Name, file.Size, copyErr)
		p.addStep(ctx, "artifact "+file.Name, copyErr.Error(), false, started)
		return Artifact{}, false
	}

	data, readErr := os.ReadFile(tempPath)
	if readErr != nil {
		log.Printf("opencode: artifact read failed: workflowId=%s name=%s size=%d err=%s", workflowIDFunc(ctx), file.Name, file.Size, readErr)
		p.addStep(ctx, "artifact "+file.Name, readErr.Error(), false, started)
		return Artifact{}, false
	}

	if len(data) == 0 || len(data) > maxArtifactBytes {
		p.addStep(ctx, "artifact "+file.Name, "unexpected size", false, started)
		return Artifact{}, false
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		p.addStep(ctx, "artifact "+file.Name, "content is not valid text", false, started)
		return Artifact{}, false
	}

	workspaceID := strings.TrimSpace(cmd.WorkspaceID)

	var lastErr error
	for attempt := 0; attempt < artifactAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(artifactRetryDelay):
			case <-ctx.Done():
				p.addStep(ctx, "artifact "+file.Name, ctx.Err().Error(), false, started)
				return Artifact{}, false
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, artifactRequestTimeout)
		art, retry, sendErr := sender.send(attemptCtx, workspaceID, file.Name, contentType, data)
		cancel()

		if sendErr != nil {
			lastErr = sendErr
		}
		if !retry {
			if sendErr != nil {
				break
			}
			log.Printf("opencode: artifact uploaded: workflowId=%s name=%s size=%d", workflowIDFunc(ctx), file.Name, file.Size)
			p.addStep(ctx, "artifact "+file.Name, strconv.Itoa(file.Size)+" bytes", true, started)
			return art, true
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("artifact: upload failed")
	}
	log.Printf("opencode: artifact upload failed: workflowId=%s name=%s size=%d err=%s", workflowIDFunc(ctx), file.Name, file.Size, lastErr)
	p.addStep(ctx, "artifact "+file.Name, lastErr.Error(), false, started)

	return Artifact{}, false
}
