// Package threat хранит актуальный снимок ресурсов сервера раннера
// (память, диски, CPU) и решает, нужно ли блокировать ресурсоёмкие
// активности. Снимок обновляется фоновым health-циклом пакета monitor,
// а читается из активностей Temporal.
package threat

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Уровни угрозы по потреблению ресурсов.
const (
	// LevelOK — потребление ниже жёлтого порога (по умолчанию < 70%).
	LevelOK = "ok"
	// LevelWarning — жёлтый уровень (>= 70%).
	LevelWarning = "warning"
	// LevelCritical — красный уровень (>= 85%).
	LevelCritical = "critical"
	// LevelBlocker — чёрный уровень (>= 90%): ресурсоёмкие активности запрещены.
	LevelBlocker = "blocker"
)

// BlockErrorKey — ключ ошибки, который backend переводит в локализованный
// текст для пользователя.
const BlockErrorKey = "runner.resources.blocker"

const (
	defaultWarningPercent  = 70.0
	defaultCriticalPercent = 85.0
	defaultBlockerPercent  = 90.0
)

// Переменные окружения для переопределения порогов (проценты потребления).
const (
	envWarningPercent  = "RUNNER_THREAT_WARNING_PERCENT"
	envCriticalPercent = "RUNNER_THREAT_CRITICAL_PERCENT"
	envBlockerPercent  = "RUNNER_THREAT_BLOCKER_PERCENT"
)

// Thresholds — границы уровней угрозы в процентах потребления ресурса.
type Thresholds struct {
	Warning  float64
	Critical float64
	Blocker  float64
}

// DefaultThresholds возвращает пороги 70% / 85% / 90%.
func DefaultThresholds() Thresholds {
	return Thresholds{
		Warning:  defaultWarningPercent,
		Critical: defaultCriticalPercent,
		Blocker:  defaultBlockerPercent,
	}
}

// Valid проверяет, что пороги упорядочены и лежат в диапазоне (0, 100].
func (t Thresholds) Valid() bool {
	if t.Warning <= 0 || t.Critical <= 0 || t.Blocker <= 0 {
		return false
	}

	if t.Blocker > 100 {
		return false
	}

	return t.Warning < t.Critical && t.Critical < t.Blocker
}

// ThresholdsFromEnv читает пороги из окружения. При отсутствии или
// некорректных значениях используются пороги по умолчанию.
func ThresholdsFromEnv() Thresholds {
	result := DefaultThresholds()

	if value, ok := envPercent(envWarningPercent); ok {
		result.Warning = value
	}
	if value, ok := envPercent(envCriticalPercent); ok {
		result.Critical = value
	}
	if value, ok := envPercent(envBlockerPercent); ok {
		result.Blocker = value
	}

	if !result.Valid() {
		return DefaultThresholds()
	}

	return result
}

// envPercent читает процент из переменной окружения.
func envPercent(name string) (float64, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, false
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 || value > 100 {
		return 0, false
	}

	return value, true
}

// LevelForPercent возвращает уровень угрозы для процента потребления.
func (t Thresholds) LevelForPercent(percent float64) string {
	switch {
	case percent >= t.Blocker:
		return LevelBlocker
	case percent >= t.Critical:
		return LevelCritical
	case percent >= t.Warning:
		return LevelWarning
	default:
		return LevelOK
	}
}

// CpuStat — загрузка CPU и load average хоста.
type CpuStat struct {
	UsagePercent float64 `json:"usagePercent"`
	Cores        int     `json:"cores"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
}

// MemoryStat — потребление оперативной памяти хоста.
type MemoryStat struct {
	TotalBytes     int64   `json:"totalBytes"`
	UsedBytes      int64   `json:"usedBytes"`
	AvailableBytes int64   `json:"availableBytes"`
	UsedPercent    float64 `json:"usedPercent"`
	Level          string  `json:"level"`
}

// DiskStat — потребление одного диска/точки монтирования.
type DiskStat struct {
	Path           string  `json:"path"`
	TotalBytes     int64   `json:"totalBytes"`
	UsedBytes      int64   `json:"usedBytes"`
	AvailableBytes int64   `json:"availableBytes"`
	UsedPercent    float64 `json:"usedPercent"`
	Level          string  `json:"level"`
}

// DockerDiskUsage — занятое место Docker (образы, контейнеры, тома, кэш сборки).
type DockerDiskUsage struct {
	LayersBytes      int64 `json:"layersBytes"`
	ContainersBytes  int64 `json:"containersBytes"`
	VolumesBytes     int64 `json:"volumesBytes"`
	BuildCacheBytes  int64 `json:"buildCacheBytes"`
	TotalBytes       int64 `json:"totalBytes"`
	ReclaimableBytes int64 `json:"reclaimableBytes"`
}

// Snapshot — снимок ресурсов сервера раннера.
//
// Уровни (`level`) и признак блокировки рассчитываются раннером по порогам
// потребления памяти и дисков; CPU входит в снимок справочно.
type Snapshot struct {
	Hostname      string           `json:"hostname"`
	UptimeSeconds int64            `json:"uptimeSeconds"`
	CollectedAt   int64            `json:"collectedAt"`
	CPU           CpuStat          `json:"cpu"`
	Memory        MemoryStat       `json:"memory"`
	Disks         []DiskStat       `json:"disks"`
	Docker        *DockerDiskUsage `json:"docker,omitempty"`
	Level         string           `json:"level"`
	Blocked       bool             `json:"blocked"`
	BlockReason   string           `json:"blockReason,omitempty"`
}

var (
	mu         sync.RWMutex
	current    Snapshot
	thresholds = ThresholdsFromEnv()
)

// SetThresholds подменяет пороги. Некорректный набор откатывается к дефолтам.
// Используется тестами; в рабочем процессе пороги задаются окружением.
func SetThresholds(value Thresholds) {
	if !value.Valid() {
		value = DefaultThresholds()
	}

	mu.Lock()
	thresholds = value
	mu.Unlock()
}

// CurrentThresholds возвращает действующие пороги.
func CurrentThresholds() Thresholds {
	mu.RLock()
	defer mu.RUnlock()

	return thresholds
}

// Publish сохраняет снимок, рассчитывая уровни и признак блокировки.
// Возвращает нормализованный снимок (его же отправляет health-push).
func Publish(snapshot Snapshot) Snapshot {
	mu.Lock()
	defer mu.Unlock()

	snapshot.Memory.UsedPercent = roundPercent(snapshot.Memory.UsedPercent)
	snapshot.Memory.Level = thresholds.LevelForPercent(snapshot.Memory.UsedPercent)

	snapshot.Level = snapshot.Memory.Level

	for index := range snapshot.Disks {
		snapshot.Disks[index].UsedPercent = roundPercent(snapshot.Disks[index].UsedPercent)
		snapshot.Disks[index].Level = thresholds.LevelForPercent(snapshot.Disks[index].UsedPercent)

		if levelRank(snapshot.Disks[index].Level) > levelRank(snapshot.Level) {
			snapshot.Level = snapshot.Disks[index].Level
		}
	}

	if snapshot.CollectedAt == 0 {
		snapshot.CollectedAt = time.Now().Unix()
	}

	snapshot.Blocked = snapshot.Level == LevelBlocker
	snapshot.BlockReason = ""
	if snapshot.Blocked {
		snapshot.BlockReason = blockerReason(snapshot)
	}

	current = snapshot

	return snapshot
}

// Current возвращает последний опубликованный снимок.
func Current() Snapshot {
	mu.RLock()
	defer mu.RUnlock()

	return current
}

// Blocked сообщает, достигнут ли уровень блокировки.
func Blocked() bool {
	mu.RLock()
	defer mu.RUnlock()

	return current.Blocked
}

// BlockReason возвращает человекочитаемую причину блокировки.
func BlockReason() string {
	mu.RLock()
	defer mu.RUnlock()

	return current.BlockReason
}

// BlockedMessage сообщает, нужно ли блокировать активность, и причину.
func BlockedMessage() (bool, string) {
	mu.RLock()
	defer mu.RUnlock()

	if !current.Blocked {
		return false, ""
	}

	reason := current.BlockReason
	if reason == "" {
		reason = "недостаточно свободных ресурсов сервера"
	}

	return true, BlockErrorKey + ": " + reason
}

// Reset очищает снимок (используется в тестах).
func Reset() {
	mu.Lock()
	current = Snapshot{}
	mu.Unlock()
}

// blockerReason собирает список ресурсов, достигших уровня блокировки.
func blockerReason(snapshot Snapshot) string {
	parts := make([]string, 0, 2)

	if snapshot.Memory.Level == LevelBlocker {
		parts = append(parts, fmt.Sprintf("память %.1f%%", snapshot.Memory.UsedPercent))
	}

	for _, disk := range snapshot.Disks {
		if disk.Level != LevelBlocker {
			continue
		}

		parts = append(parts, fmt.Sprintf("диск %s %.1f%%", disk.Path, disk.UsedPercent))
	}

	if len(parts) == 0 {
		return "недостаточно свободных ресурсов сервера"
	}

	return strings.Join(parts, ", ")
}

// levelRank задаёт порядок уровней для выбора худшего.
func levelRank(level string) int {
	switch level {
	case LevelBlocker:
		return 3
	case LevelCritical:
		return 2
	case LevelWarning:
		return 1
	default:
		return 0
	}
}

// roundPercent округляет процент до двух знаков и ограничивает [0, 100].
func roundPercent(value float64) float64 {
	if math.IsNaN(value) || value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}

	return math.Round(value*100) / 100
}
