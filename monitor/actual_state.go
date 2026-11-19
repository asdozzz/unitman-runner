package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"runner/state"
	"runner/temporal/utils"
)

// dockerPSRecord — подмножество полей вывода `docker ps --format '{{json .}}'`.
// Podman может отдавать поле Name вместо Names.
type dockerPSRecord struct {
	Names string `json:"Names"`
	Name  string `json:"Name"`
	State string `json:"State"`
}

// readActualState одной командой получает фактическое состояние всех контейнеров.
func readActualState(ctx context.Context, workdir string) (map[string]state.ActualState, error) {
	output, err := utils.ExecCommandContext(ctx, workdir, []string{
		"docker", "ps", "-a", "--format", "{{json .}}",
	})
	if err != nil {
		return nil, fmt.Errorf("monitor: docker ps: %w", err)
	}

	return parseDockerPSOutput(output), nil
}

// parseDockerPSOutput разбирает построчный JSON-вывод docker ps в карту
// containerName -> фактическое состояние.
func parseDockerPSOutput(output string) map[string]state.ActualState {
	result := make(map[string]state.ActualState)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var record dockerPSRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			log.Printf("monitor: не удалось разобрать строку docker ps: %q: %v", truncate(line, 200), err)
			continue
		}

		names := record.Names
		if names == "" {
			names = record.Name
		}
		if names == "" {
			log.Printf("monitor: строка docker ps без имени контейнера: %q", truncate(line, 200))
			continue
		}

		actual := classifyDockerState(record.State)
		for _, name := range strings.Split(names, ",") {
			name = normalizeContainerName(name)
			if name == "" {
				continue
			}
			result[name] = actual
		}
	}

	return result
}

// classifyDockerState классифицирует состояние контейнера из docker ps.
func classifyDockerState(raw string) state.ActualState {
	if strings.EqualFold(strings.TrimSpace(raw), string(state.ActualRunning)) {
		return state.ActualRunning
	}

	return state.ActualStopped
}

// actualForContainer определяет фактическое состояние конкретного контейнера.
func actualForContainer(containerName string, containers map[string]state.ActualState) state.ActualState {
	name := normalizeContainerName(containerName)
	if name == "" {
		return state.ActualUnknown
	}

	if actual, ok := containers[name]; ok {
		return actual
	}

	return state.ActualAbsent
}

// normalizeContainerName убирает ведущий "/" и пробелы.
func normalizeContainerName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")

	return strings.TrimSpace(name)
}

// truncate ограничивает длину строки для логов.
func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}

	return value[:limit] + "..."
}
