package monitor

import (
	"context"
	"log"
	"time"

	"runner/state"
)

const (
	reconcilerInterval = 10 * time.Minute
	// notifyReminderSeconds — минимальный интервал между напоминаниями
	// об одном и том же расхождении.
	notifyReminderSeconds int64 = 30 * 60
)

// RunReconciler запускает bootstrap и периодическую сверку ожидаемого
// и фактического состояния контейнеров юнитов.
func RunReconciler(ctx context.Context, store state.Store, client *Client, workdir string) {
	Bootstrap(ctx, store, workdir)

	ticker := time.NewTicker(reconcilerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			reconcileOnce(ctx, store, client, workdir)
		}
	}
}

// reconcileOnce выполняет один цикл сверки.
func reconcileOnce(ctx context.Context, store state.Store, client *Client, workdir string) {
	actualMap, err := readActualState(ctx, workdir)
	if err != nil {
		log.Printf("monitor: reconciler: %v", err)
		markActualUnknown(store)
		return
	}

	items, err := store.List()
	if err != nil {
		log.Printf("monitor: reconciler: list units: %v", err)
		return
	}

	now := time.Now().Unix()
	notifications := make([]Notification, 0)

	for _, item := range items {
		actual := actualForContainer(item.ContainerName, actualMap)

		item.Actual = actual
		item.ActualCheckedAt = now
		if err := store.SetActual(item.UnitID, actual, now); err != nil {
			log.Printf("monitor: reconciler: set actual unit %q: %v", item.UnitID, err)
		}

		// Неизвестное состояние не даёт оснований для уведомлений.
		if actual == state.ActualUnknown {
			continue
		}

		if isMismatch(item.Expected, actual) {
			if item.MismatchSince == 0 {
				item.MismatchSince = now
			}

			if shouldNotifyMismatch(item, actual, now) {
				notifications = append(notifications, buildNotification(item, actual, "mismatch", now))
			}

			if err := store.Upsert(item); err != nil {
				log.Printf("monitor: reconciler: upsert unit %q: %v", item.UnitID, err)
			}
			continue
		}

		// Переход из расхождения в совпадение — уведомляем о восстановлении.
		if item.MismatchSince != 0 {
			item.MismatchSince = 0
			notifications = append(notifications, buildNotification(item, actual, "resolved", now))

			if err := store.Upsert(item); err != nil {
				log.Printf("monitor: reconciler: upsert unit %q: %v", item.UnitID, err)
			}
		}
	}

	sendNotifications(ctx, store, client, notifications, now)
}

// markActualUnknown фиксирует unknown для всех юнитов при ошибке docker ps.
func markActualUnknown(store state.Store) {
	items, err := store.List()
	if err != nil {
		log.Printf("monitor: reconciler: list units: %v", err)
		return
	}

	now := time.Now().Unix()
	for _, item := range items {
		if err := store.SetActual(item.UnitID, state.ActualUnknown, now); err != nil {
			log.Printf("monitor: reconciler: set unknown unit %q: %v", item.UnitID, err)
		}
	}
}

// isMismatch определяет расхождение ожидаемого и фактического состояний.
func isMismatch(expected state.ExpectedState, actual state.ActualState) bool {
	switch expected {
	case state.ExpectedRunning:
		return actual != state.ActualRunning
	case state.ExpectedAbsent:
		return actual != state.ActualAbsent
	default:
		return false
	}
}

// shouldNotifyMismatch решает, нужно ли отправлять уведомление о расхождении:
// первый mismatch, смена фактического состояния или напоминание раз в 30 минут.
func shouldNotifyMismatch(item state.UnitState, actual state.ActualState, now int64) bool {
	if item.LastNotifiedActual == "" {
		return true
	}
	if item.LastNotifiedActual != actual {
		return true
	}
	if now-item.LastNotifiedAt >= notifyReminderSeconds {
		return true
	}

	return false
}

// buildNotification формирует уведомление по записи состояния.
func buildNotification(item state.UnitState, actual state.ActualState, eventType string, now int64) Notification {
	return Notification{
		UnitID:        item.UnitID,
		ProjectID:     item.ProjectID,
		UnitName:      item.UnitName,
		ProjectName:   item.ProjectName,
		ExpectedState: string(item.Expected),
		ActualState:   string(actual),
		EventType:     eventType,
		DetectedAt:    now,
	}
}

// sendNotifications отправляет батч одним запросом и фиксирует факт отправки
// только после успешного ответа.
func sendNotifications(ctx context.Context, store state.Store, client *Client, notifications []Notification, now int64) {
	if len(notifications) == 0 {
		return
	}
	if client == nil {
		log.Printf("monitor: reconciler: клиент не задан, уведомления не отправлены (%d)", len(notifications))
		return
	}

	payload := NotificationsPayload{
		RunnerID:      client.RunnerID,
		Notifications: notifications,
	}

	if err := client.SendNotifications(ctx, payload); err != nil {
		log.Printf("monitor: reconciler: send notifications: %v", err)
		return
	}

	for _, notification := range notifications {
		if err := store.MarkNotified(notification.UnitID, state.ActualState(notification.ActualState), now); err != nil {
			log.Printf("monitor: reconciler: mark notified unit %q: %v", notification.UnitID, err)
		}
	}
}
