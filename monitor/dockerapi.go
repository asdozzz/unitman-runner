package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// dockerHostEnvVar — стандартная переменная docker CLI для адреса daemon.
	dockerHostEnvVar = "DOCKER_HOST"
	// defaultDockerHost — локальный unix-сокет docker.
	defaultDockerHost = "unix:///var/run/docker.sock"
	// dockerAPIPseudoHost — адрес для запросов через unix-сокет: транспорт
	// игнорирует хост и соединяется с сокетом.
	dockerAPIPseudoHost = "http://docker"
	// dockerAPITimeout — таймаут одного запроса к Docker API.
	dockerAPITimeout = 5 * time.Second
	// maxDockerAPIBody — максимальный размер ответа Docker API.
	maxDockerAPIBody = 4 * 1024 * 1024
)

// dockerAPIClient — минимальный клиент Docker Engine API, который отдаёт
// точные байтовые значения памяти контейнера (в отличие от humanized-строк
// `docker stats`).
type dockerAPIClient struct {
	httpClient *http.Client
	baseURL    string
}

// newDockerAPIClient создаёт клиент по DOCKER_HOST (unix:// или tcp:///http://).
// Если конфигурация не поддерживается (npipe, ssh, TLS), возвращает nil:
// health-push при этом уходит без обогащения, как и раньше.
func newDockerAPIClient() *dockerAPIClient {
	host := strings.TrimSpace(os.Getenv(dockerHostEnvVar))
	if host == "" {
		host = defaultDockerHost
	}

	switch {
	case strings.HasPrefix(host, "unix://"):
		socketPath := strings.TrimPrefix(host, "unix://")
		if socketPath == "" {
			return nil
		}

		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: dockerAPITimeout}
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		}

		return &dockerAPIClient{
			httpClient: &http.Client{Transport: transport, Timeout: dockerAPITimeout},
			baseURL:    dockerAPIPseudoHost,
		}
	case strings.HasPrefix(host, "tcp://"), strings.HasPrefix(host, "http://"):
		addr := strings.TrimPrefix(host, "tcp://")
		addr = strings.TrimPrefix(addr, "http://")
		if addr == "" {
			return nil
		}

		return &dockerAPIClient{
			httpClient: &http.Client{Timeout: dockerAPITimeout},
			baseURL:    "http://" + addr,
		}
	default:
		return nil
	}
}

// containerInspect — подмножество ответа GET /containers/{name}/json.
type containerInspect struct {
	HostConfig struct {
		Memory int64 `json:"Memory"`
	} `json:"HostConfig"`
}

// containerStats — подмножество ответа
// GET /containers/{name}/stats?stream=false.
type containerStats struct {
	MemoryStats struct {
		Usage int64            `json:"usage"`
		Limit int64            `json:"limit"`
		Stats map[string]int64 `json:"stats"`
	} `json:"memory_stats"`
}

// dockerServerInfo — подмножество ответа GET /info.
type dockerServerInfo struct {
	DockerRootDir string `json:"DockerRootDir"`
	NCPU          int    `json:"NCPU"`
	MemTotal      int64  `json:"MemTotal"`
}

// dockerSystemDF — подмножество ответа GET /system/df.
// Размеры приходят числами (в отличие от humanized-вывода `docker system df`).
type dockerSystemDF struct {
	LayersSize int64 `json:"LayersSize"`
	Images     []struct {
		Size int64 `json:"Size"`
	} `json:"Images"`
	Containers []struct {
		SizeRw int64 `json:"SizeRw"`
	} `json:"Containers"`
	Volumes []struct {
		UsageData *struct {
			Size int64 `json:"Size"`
		} `json:"UsageData"`
	} `json:"Volumes"`
	BuildCache []struct {
		Size int64 `json:"Size"`
	} `json:"BuildCache"`
}

// containersSize суммирует записываемый слой всех контейнеров.
func (d *dockerSystemDF) containersSize() int64 {
	var total int64

	for _, container := range d.Containers {
		total += container.SizeRw
	}

	return total
}

// volumesSize суммирует занятое место томов.
func (d *dockerSystemDF) volumesSize() int64 {
	var total int64

	for _, volume := range d.Volumes {
		if volume.UsageData == nil {
			continue
		}

		total += volume.UsageData.Size
	}

	return total
}

// buildCacheSize суммирует кэш сборки.
func (d *dockerSystemDF) buildCacheSize() int64 {
	var total int64

	for _, entry := range d.BuildCache {
		total += entry.Size
	}

	return total
}

// serverInfo запрашивает у daemon сведения о хосте (каталог Docker и т.п.).
func (c *dockerAPIClient) serverInfo(ctx context.Context) (*dockerServerInfo, error) {
	var out dockerServerInfo
	if err := c.getJSON(ctx, "/info", &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// systemDiskUsage запрашивает занятое место Docker по типам данных.
func (c *dockerAPIClient) systemDiskUsage(ctx context.Context) (*dockerSystemDF, error) {
	var out dockerSystemDF
	if err := c.getJSON(ctx, "/system/df", &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// inspectContainer запрашивает у daemon настройки контейнера.
func (c *dockerAPIClient) inspectContainer(ctx context.Context, name string) (*containerInspect, error) {
	var out containerInspect
	if err := c.getJSON(ctx, "/containers/"+url.PathEscape(name)+"/json", &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// containerStatsSample запрашивает один снимок статистики контейнера.
func (c *dockerAPIClient) containerStatsSample(ctx context.Context, name string) (*containerStats, error) {
	var out containerStats
	if err := c.getJSON(ctx, "/containers/"+url.PathEscape(name)+"/stats?stream=false", &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// getJSON выполняет GET-запрос к Docker API и разбирает JSON-ответ.
func (c *dockerAPIClient) getJSON(ctx context.Context, path string, out any) error {
	if c == nil || c.httpClient == nil {
		return fmt.Errorf("docker api: клиент не инициализирован")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("docker api: create request %s: %w", path, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("docker api: GET %s: %w", path, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker api: GET %s: unexpected status %d", path, resp.StatusCode)
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerAPIBody)).Decode(out); err != nil {
		return fmt.Errorf("docker api: decode %s: %w", path, err)
	}

	return nil
}

// dockerMemUsage повторяет расчёт `docker stats`: из memory_stats.usage
// вычитается неактивный file-cache (cgroup v1 — total_inactive_file,
// cgroup v2 — inactive_file), чтобы значение совпадало с выводом docker.
func dockerMemUsage(sample *containerStats) int64 {
	if sample == nil {
		return 0
	}

	usage := sample.MemoryStats.Usage
	if usage <= 0 {
		return 0
	}

	if cached, ok := sample.MemoryStats.Stats["total_inactive_file"]; ok && cached > 0 && cached < usage {
		return usage - cached
	}
	if cached, ok := sample.MemoryStats.Stats["inactive_file"]; ok && cached > 0 && cached < usage {
		return usage - cached
	}

	return usage
}
