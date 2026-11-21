package monitor

import (
	"context"
	"log"
	"strings"
	"time"

	"runner/temporal/utils"
)

const cleanupInterval = 2 * time.Hour

// RunCleanup каждые два часа чистит неиспользуемые docker-образы и кэш сборки.
func RunCleanup(ctx context.Context, workdir string) {
	cleanupOnce(ctx, workdir)

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			cleanupOnce(ctx, workdir)
		}
	}
}

// cleanupOnce выполняет один цикл очистки. Ошибки логируются, цикл продолжается.
func cleanupOnce(ctx context.Context, workdir string) {
	commands := [][]string{
		// label!=... сохраняет образы с меткой unitman.keep=true: без фильтра
		// prune -a удаляет базовый dind-образ юнита, который не используется
		// ни одним контейнером напрямую, и следующая сборка тянет старую версию.
		{"docker", "image", "prune", "-af", "--filter", "label!=unitman.keep=true"},
		{"docker", "builder", "prune", "-af"},
	}

	for _, args := range commands {
		if ctx.Err() != nil {
			return
		}

		if _, err := utils.ExecCommandContext(ctx, workdir, args); err != nil {
			log.Printf("monitor: cleanup: %s: %v", strings.Join(args, " "), err)
		}
	}
}
