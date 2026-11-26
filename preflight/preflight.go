package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// RequiredRuntime — runtime, без которого юнит не запустится.
	RequiredRuntime = "sysbox-runc"
	// SkipEnv отключает проверку. Только для разработки и CI без sysbox.
	SkipEnv = "UNITMAN_SKIP_PREFLIGHT"
)

// Check проверяет, что Docker доступен и в нём зарегистрирован sysbox-runc.
// Проверка обязательна для on-premise поставки: fallback на privileged dind
// не предусмотрен.
func Check() error {
	if strings.TrimSpace(os.Getenv(SkipEnv)) == "1" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .Runtimes}}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker недоступен: запустите `docker info` и проверьте права доступа к сокету: %w", err)
	}

	runtimes := map[string]json.RawMessage{}
	if err := json.Unmarshal(output, &runtimes); err != nil {
		return fmt.Errorf("разобрать список docker runtimes: %w", err)
	}

	if !HasRuntime(runtimes, RequiredRuntime) {
		return fmt.Errorf(
			"docker runtime %q не найден: установите sysbox (https://github.com/nestybox/sysbox); "+
				"для локальной отладки без sysbox задайте %s=1",
			RequiredRuntime,
			SkipEnv,
		)
	}

	return nil
}

// HasRuntime сообщает, зарегистрирован ли runtime в выводе `docker info`.
func HasRuntime(runtimes map[string]json.RawMessage, name string) bool {
	_, ok := runtimes[name]

	return ok
}
