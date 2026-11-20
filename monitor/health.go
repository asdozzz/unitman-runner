package monitor

import (
	"context"
	"log"
	"os"
	"time"

	"runner/state"
	"runner/temporal/utils"
)

const (
	healthInterval = time.Minute
	// maxDockerStatsSize — максимальный размер блока docker stats, который
	// имеет смысл отправлять в health-push.
	maxDockerStatsSize = 2 * 1024 * 1024
)

// RunHealth раз в минуту отправляет health-push на backend.
func RunHealth(ctx context.Context, store state.Store, client *Client, workdir string) {
	_ = store

	// Клиент Docker API создаётся один раз: при неподдерживаемой конфигурации
	// он равен nil, и health-push уходит без обогащения статистики.
	api := newDockerAPIClient()

	sendHealthOnce(ctx, client, workdir, api)

	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			sendHealthOnce(ctx, client, workdir, api)
		}
	}
}

// sendHealthOnce собирает и отправляет один health-push.
func sendHealthOnce(ctx context.Context, client *Client, workdir string, api *dockerAPIClient) {
	if client == nil {
		log.Printf("monitor: health: клиент не задан")
		return
	}

	payload := HealthPayload{
		RunnerID:  client.RunnerID,
		TaskQueue: client.TaskQueue,
		// Ошибка docker stats не отменяет отправку: раннер продолжает работать.
		Active:      true,
		DockerStats: collectUnitDockerStats(ctx, workdir, api),
		MemInfo:     readMemInfo(),
		Unixtime:    time.Now().Unix(),
	}

	if err := client.SendHealth(ctx, payload); err != nil {
		log.Printf("monitor: health: %v", err)
	}
}

// collectUnitDockerStats возвращает вывод `docker stats`, в котором для
// юнит-контейнеров MemPerc/MemUsage пересчитаны относительно лимита памяти
// юнита (а не от общей памяти хоста).
func collectUnitDockerStats(ctx context.Context, workdir string, api *dockerAPIClient) string {
	raw := collectDockerStats(ctx, workdir)
	if raw == "" {
		return raw
	}

	return enrichDockerStats(ctx, raw, unitMemoryLimits(workdir), api)
}

// collectDockerStats возвращает вывод `docker stats --no-stream --format json`.
func collectDockerStats(ctx context.Context, workdir string) string {
	output, err := utils.ExecCommandContext(ctx, workdir, []string{
		"docker", "stats", "--no-stream", "--format", "json",
	})
	if err != nil {
		log.Printf("monitor: health: docker stats: %v", err)
		return ""
	}

	if len(output) > maxDockerStatsSize {
		log.Printf("monitor: health: docker stats слишком большой (%d байт > %d), отправляю пустые данные", len(output), maxDockerStatsSize)
		return ""
	}

	return output
}

// readMemInfo читает содержимое /proc/meminfo.
func readMemInfo() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		log.Printf("monitor: health: read /proc/meminfo: %v", err)
		return ""
	}

	return string(data)
}
