package config

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ConfigTemplatePath — шаблон, из которого может создаваться рабочий
	// config.json (например, для ручной настройки).
	ConfigTemplatePath = "config.json.dist"
	// ConfigPath — путь к конфигу, который читает serve.
	ConfigPath = "config.json"

	// RegistrationPath — публичный эндпоинт регистрации раннера.
	RegistrationPath = "/api/runner/register"
	// RegistrationTimeout — таймаут HTTP-запроса регистрации.
	RegistrationTimeout = 10 * time.Second

	// DefaultTemporalPort — порт Temporal, если backend не вернул свой.
	DefaultTemporalPort = 7233
	// DefaultAPIPort — порт backend API, если backend не вернул свой.
	DefaultAPIPort = 8082
	// DefaultRegistryPort — порт реестра, если backend не вернул свой.
	DefaultRegistryPort = 5000

	// DefaultVersion — версия раннера, если она не задана при сборке.
	DefaultVersion = "dev"

	// RunnerIDHeader — заголовок идентификатора раннера во всех callback-ах.
	RunnerIDHeader = "X-Runner-Id"
	// RunnerSignatureHeader — заголовок HMAC-SHA256 подписи callback-а.
	RunnerSignatureHeader = "X-Runner-Signature"
)

// RegistrationRequest — тело публичного `POST /api/runner/register`.
//
// Регистрация не требует аутентификации: никакие секреты и подписи не
// отправляются.
type RegistrationRequest struct {
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
}

// RegistrationPorts — порты, выданные backend-ом при регистрации.
type RegistrationPorts struct {
	API      int `json:"api"`
	Temporal int `json:"temporal"`
	Registry int `json:"registry"`
}

// RegistrationResponse — успешный ответ backend-а (201 Created).
type RegistrationResponse struct {
	RunnerID     string            `json:"runnerId"`
	TaskQueue    string            `json:"taskQueue"`
	Secret       string            `json:"secret"`
	RegistryHost string            `json:"registryHost"`
	Ports        RegistrationPorts `json:"ports"`
}

// InitOptions — входные данные `runner init`.
//
// Host — единственное, что спрашивается у пользователя. Остальные поля
// заполняются из ответа backend-а.
type InitOptions struct {
	Host       string
	ConfigPath string
	HTTPClient *http.Client
	Hostname   string
	Version    string

	// registerURL — тестовый шов для httptest: позволяет направить запрос на
	// локальный сервер, не меняя публичный Host.
	registerURL string
}

// NormalizeHost приводит введённый пользователем host к виду host[:port] без
// схемы и завершающего слэша.
func NormalizeHost(raw string) string {
	host := strings.TrimSpace(raw)
	if host == "" {
		return "localhost"
	}
	if index := strings.Index(host, "://"); index >= 0 {
		host = host[index+3:]
	}
	host = strings.TrimSpace(strings.TrimRight(host, "/"))
	if host == "" {
		return "localhost"
	}

	return host
}

// RegistrationURL собирает публичный URL регистрации: http://<host>:8082/...
func RegistrationURL(host string) string {
	return buildHTTPURL(NormalizeHost(host), DefaultAPIPort) + RegistrationPath
}

// RegisterRunner регистрирует раннер на публичном эндпоинте backend-а.
//
// Запрос не подписывается: backend принимает регистрацию без аутентификации.
// Ответ проверяется на согласованность taskQueue/runnerId и формат секрета.
func RegisterRunner(ctx context.Context, registerURL string, request RegistrationRequest, httpClient *http.Client) (RegistrationResponse, error) {
	var response RegistrationResponse

	request.Hostname = strings.TrimSpace(request.Hostname)
	request.Version = strings.TrimSpace(request.Version)
	if request.Version == "" {
		request.Version = DefaultVersion
	}

	rawBody, err := json.Marshal(request)
	if err != nil {
		return response, fmt.Errorf("marshal registration request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, bytes.NewReader(rawBody))
	if err != nil {
		return response, fmt.Errorf("create registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if httpClient == nil {
		httpClient = &http.Client{Timeout: RegistrationTimeout}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return response, fmt.Errorf("POST %s: %w", registerURL, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if err != nil {
		return response, fmt.Errorf("read registration response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return response, registrationStatusError(resp.StatusCode, body)
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return response, fmt.Errorf("decode registration response: %w", err)
	}

	response.RunnerID = strings.TrimSpace(response.RunnerID)
	response.TaskQueue = strings.TrimSpace(response.TaskQueue)
	response.Secret = strings.TrimSpace(response.Secret)
	response.RegistryHost = strings.TrimSpace(response.RegistryHost)

	if response.RunnerID == "" {
		return response, errors.New("registration response has empty runnerId")
	}
	if response.TaskQueue != response.RunnerID {
		return response, fmt.Errorf("registration response taskQueue %q != runnerId %q", response.TaskQueue, response.RunnerID)
	}
	if err := validateRunnerSecret(response.Secret); err != nil {
		return response, err
	}

	return response, nil
}

// validateRunnerSecret проверяет, что backend выдал 64 hex-символа.
func validateRunnerSecret(secret string) error {
	if secret == "" {
		return errors.New("registration response has empty secret")
	}
	if len(secret) != 64 {
		return fmt.Errorf("registration response secret must be 64 hex chars, got %d", len(secret))
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return errors.New("registration response secret is not valid hex")
	}

	return nil
}

// RunInit выполняет полный сценарий `runner init`: регистрирует раннер и
// записывает config.json с правами 0600.
func RunInit(ctx context.Context, opts InitOptions) (Configuration, error) {
	var configuration Configuration

	opts.Host = NormalizeHost(opts.Host)
	if strings.TrimSpace(opts.Version) == "" {
		opts.Version = DefaultVersion
	}

	registerURL := opts.registerURL
	if registerURL == "" {
		registerURL = RegistrationURL(opts.Host)
	}

	response, err := RegisterRunner(ctx, registerURL, RegistrationRequest{
		Hostname: opts.Hostname,
		Version:  opts.Version,
	}, opts.HTTPClient)
	if err != nil {
		return configuration, fmt.Errorf("регистрация раннера: %w", err)
	}

	apiPort := response.Ports.API
	if apiPort <= 0 {
		apiPort = DefaultAPIPort
	}
	temporalPort := response.Ports.Temporal
	if temporalPort <= 0 {
		temporalPort = DefaultTemporalPort
	}
	registryPort := response.Ports.Registry
	if registryPort <= 0 {
		registryPort = DefaultRegistryPort
	}

	// Порт ответа backend-а — основной источник. Явный порт в Host относится
	// к адресу регистрации и не должен подменять выданные порты сервисов.
	serviceHost := hostnameOnly(opts.Host)

	configuration = Configuration{
		TemporalHost: buildHostPort(serviceHost, temporalPort),
		ApiHost:      buildHTTPURL(serviceHost, apiPort),
		RunnerID:     response.RunnerID,
		RunnerSecret: response.Secret,
		RegistryHost: buildRegistryHost(response.RegistryHost, serviceHost, registryPort),
	}

	if err := configuration.Validate(); err != nil {
		return configuration, err
	}

	target := opts.ConfigPath
	if target == "" {
		target = ConfigPath
	}
	if err := WriteConfig(target, configuration); err != nil {
		return configuration, err
	}

	return configuration, nil
}

// buildRegistryHost собирает Docker-ссылку реестра: host:port без схемы.
//
// Docker image reference не допускает scheme, поэтому registryHost в
// config.json всегда имеет вид host:port. Схема из ответа backend-а
// отбрасывается, а порт берётся из ports.registry (или уже указанный явно).
// Пустой registryHost заменяется host-ом регистрации и registry-портом.
func buildRegistryHost(raw, fallbackHost string, port int) string {
	registryHost := strings.TrimSpace(raw)
	if registryHost == "" {
		return buildHostPort(fallbackHost, port)
	}

	return buildHostPort(registryHost, port)
}

// WriteConfig атомарно сохраняет конфигурацию в JSON с правами 0600.
//
// Файл пишется во временный файл в той же директории и переименовывается,
// чтобы читатель никогда не увидел частично записанный конфиг (в нём лежит
// секрет раннера).
func WriteConfig(path string, configuration Configuration) error {
	data, err := json.MarshalIndent(configuration, "", "    ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write %s: %w", tempPath, err)
	}
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod %s: %w", tempPath, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tempPath, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}

	// chmod после rename — страховка для файловых систем, где права временного
	// файла могли не сохраниться.
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}

	return nil
}

// registrationStatusError превращает не-201 ответ backend-а в понятную ошибку.
func registrationStatusError(status int, body []byte) error {
	suffix := ""
	if code := registrationErrorCode(body); code != "" {
		suffix = " (" + code + ")"
	}

	switch status {
	case http.StatusBadRequest:
		return fmt.Errorf("backend отклонил регистрацию: невалидный payload%s", suffix)
	case http.StatusNotFound:
		return fmt.Errorf("backend вернул 404 при регистрации%s: проверьте host", suffix)
	default:
		if status >= 500 {
			return fmt.Errorf("backend вернул ошибку %d%s при регистрации", status, suffix)
		}

		return fmt.Errorf("backend вернул неожиданный статус %d%s", status, suffix)
	}
}

// registrationErrorCode извлекает код ошибки из тела ответа, если он есть.
func registrationErrorCode(body []byte) string {
	var payload struct {
		Code      string `json:"code"`
		ErrorCode string `json:"errorCode"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	for _, candidate := range []string{payload.Code, payload.ErrorCode, payload.Error} {
		if candidate != "" {
			return candidate
		}
	}

	return ""
}

// splitHost отделяет схему от host[:port].
func splitHost(host string) (scheme, hostPort string) {
	scheme = "http"
	if strings.Contains(host, "://") {
		if parsed, err := url.Parse(host); err == nil && parsed.Host != "" {
			scheme = parsed.Scheme
			hostPort = parsed.Host
		}
	}
	if hostPort == "" {
		hostPort = host
	}

	return scheme, strings.TrimSuffix(hostPort, "/")
}

// hostnameOnly отбрасывает порт, оставляя только host.
func hostnameOnly(host string) string {
	_, hostPort := splitHost(host)
	if hostname, _, err := net.SplitHostPort(hostPort); err == nil {
		return hostname
	}

	return hostPort
}

// buildHTTPURL собирает URL со схемой и портом. Если host уже содержит порт,
// он сохраняется как есть.
func buildHTTPURL(host string, port int) string {
	scheme, hostPort := splitHost(host)
	if _, _, err := net.SplitHostPort(hostPort); err == nil {
		return scheme + "://" + hostPort
	}

	return fmt.Sprintf("%s://%s:%d", scheme, hostPort, port)
}

// buildHostPort собирает host:port без схемы.
func buildHostPort(host string, port int) string {
	_, hostPort := splitHost(host)
	if _, _, err := net.SplitHostPort(hostPort); err == nil {
		return hostPort
	}

	return fmt.Sprintf("%s:%d", hostPort, port)
}
