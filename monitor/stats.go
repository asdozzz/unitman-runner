package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// unitMemoryLimitEnvKey — переменная .env юнита с лимитом памяти.
	unitMemoryLimitEnvKey = "UNITMAN_MEMORY_LIMIT"
	// defaultUnitMemoryLimit — дефолт из compose:
	// `${UNITMAN_MEMORY_LIMIT:-3g}` в compose.dind.yaml / compose.podman.yaml.
	defaultUnitMemoryLimit = "3g"
)

// unitMemoryLimits собирает карту containerName -> лимит памяти в байтах
// из projects/<projectId>/units/<unitId>/.env. Имя контейнера строится так же,
// как в bootstrap: <UNITMAN_UNIT_NAME>.<UNITMAN_PROJECT_NAME>.
func unitMemoryLimits(workdir string) map[string]int64 {
	limits := make(map[string]int64)

	projectsRoot := filepath.Join(workdir, "projects")
	projects, err := os.ReadDir(projectsRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("monitor: stats: read %q: %v", projectsRoot, err)
		}
		return limits
	}

	defaultLimit, err := parseComposeSize(defaultUnitMemoryLimit)
	if err != nil {
		defaultLimit = 0
	}

	for _, project := range projects {
		if !project.IsDir() {
			continue
		}

		unitsRoot := filepath.Join(projectsRoot, project.Name(), "units")
		units, err := os.ReadDir(unitsRoot)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("monitor: stats: read %q: %v", unitsRoot, err)
			}
			continue
		}

		for _, unitEntry := range units {
			if !unitEntry.IsDir() {
				continue
			}

			unitPath := filepath.Join(unitsRoot, unitEntry.Name())
			env, err := readEnvFile(filepath.Join(unitPath, ".env"))
			if err != nil {
				continue
			}

			unitName := env[unitNameEnvKey]
			projectName := env[projectNameEnvKey]
			if unitName == "" || projectName == "" {
				continue
			}

			limit, err := parseComposeSize(env[unitMemoryLimitEnvKey])
			if err != nil || limit <= 0 {
				limit = defaultLimit
			}
			if limit <= 0 {
				continue
			}

			limits[unitName+"."+projectName] = limit
		}
	}

	return limits
}

// parseComposeSize разбирает значение лимита compose (`3g`, `3072m`,
// `262144k`, `3221225472`) в байты. Множители двоичные — как в docker.
func parseComposeSize(value string) (int64, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return 0, fmt.Errorf("пустое значение размера")
	}

	multiplier := int64(1)
	for _, unit := range []struct {
		suffix string
		mult   int64
	}{
		{"gib", 1 << 30}, {"gb", 1 << 30}, {"g", 1 << 30},
		{"mib", 1 << 20}, {"mb", 1 << 20}, {"m", 1 << 20},
		{"kib", 1 << 10}, {"kb", 1 << 10}, {"k", 1 << 10},
		{"b", 1},
	} {
		if strings.HasSuffix(trimmed, unit.suffix) {
			multiplier = unit.mult
			trimmed = strings.TrimSuffix(trimmed, unit.suffix)
			break
		}
	}

	number, err := strconv.ParseFloat(strings.TrimSpace(trimmed), 64)
	if err != nil {
		return 0, fmt.Errorf("некорректный размер: %w", err)
	}
	if number < 0 {
		return 0, fmt.Errorf("отрицательный размер")
	}

	return int64(number * float64(multiplier)), nil
}

// humanizeBytes форматирует байты в двоичные единицы, как docker stats
// (0B, 512MiB, 3GiB), оставляя до двух знаков после запятой.
func humanizeBytes(size int64) string {
	if size <= 0 {
		return "0B"
	}

	for _, unit := range []struct {
		suffix string
		value  float64
	}{
		{"TiB", 1 << 40},
		{"GiB", 1 << 30},
		{"MiB", 1 << 20},
		{"KiB", 1 << 10},
	} {
		scaled := math.Round(float64(size)/unit.value*100) / 100
		if scaled >= 1 {
			return strconv.FormatFloat(scaled, 'f', -1, 64) + unit.suffix
		}
	}

	return strconv.FormatInt(size, 10) + "B"
}

// enrichDockerStats переписывает MemPerc/MemUsage для строк docker stats,
// относящихся к юнит-контейнерам, считая процент от лимита памяти юнита.
// Остальные строки и ошибки Docker API оставляют вывод без изменений.
func enrichDockerStats(ctx context.Context, raw string, limits map[string]int64, api *dockerAPIClient) string {
	if raw == "" || api == nil || len(limits) == 0 {
		return raw
	}

	lines := strings.Split(raw, "\n")
	enriched := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		record := make(map[string]any)
		if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
			enriched = append(enriched, line)
			continue
		}

		name, _ := record["Name"].(string)
		limit, isUnit := limits[name]
		if !isUnit || limit <= 0 {
			enriched = append(enriched, line)
			continue
		}

		updated, err := enrichUnitStatsLine(ctx, record, name, limit, api)
		if err != nil {
			log.Printf("monitor: stats: обогащение %q: %v", name, err)
			enriched = append(enriched, line)
			continue
		}

		enriched = append(enriched, updated)
	}

	return strings.Join(enriched, "\n")
}

// enrichUnitStatsLine запрашивает точные значения памяти у Docker API и
// подменяет MemPerc/MemUsage в строке статистики.
func enrichUnitStatsLine(ctx context.Context, record map[string]any, name string, configuredLimit int64, api *dockerAPIClient) (string, error) {
	sample, err := api.containerStatsSample(ctx, name)
	if err != nil {
		return "", err
	}

	limit := resolveMemoryLimit(ctx, name, configuredLimit, api)
	if limit <= 0 {
		return "", fmt.Errorf("не удалось определить лимит памяти")
	}

	usage := dockerMemUsage(sample)

	record["MemPerc"] = formatMemoryPercent(usage, limit)
	record["MemUsage"] = humanizeBytes(usage) + " / " + humanizeBytes(limit)

	updated, err := json.Marshal(record)
	if err != nil {
		return "", err
	}

	return string(updated), nil
}

// resolveMemoryLimit определяет лимит контейнера: сначала фактический
// HostConfig.Memory, затем лимит из .env юнита (на случай, если рантайм
// лимит не применил).
func resolveMemoryLimit(ctx context.Context, name string, configuredLimit int64, api *dockerAPIClient) int64 {
	inspect, err := api.inspectContainer(ctx, name)
	if err == nil && inspect.HostConfig.Memory > 0 {
		return inspect.HostConfig.Memory
	}

	return configuredLimit
}

// formatMemoryPercent форматирует процент памяти в стиле docker stats.
func formatMemoryPercent(usage, limit int64) string {
	if limit <= 0 {
		return "0.00%"
	}

	percent := float64(usage) / float64(limit) * 100

	return strconv.FormatFloat(percent, 'f', 2, 64) + "%"
}
