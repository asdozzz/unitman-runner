package monitor

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"runner/threat"
)

const (
	// cpuSampleInterval — окно замера загрузки CPU по /proc/stat.
	cpuSampleInterval = 500 * time.Millisecond
	// resourcesInterval — период обновления снимка ресурсов. Короче
	// health-push (1 минута), чтобы блокировка включалась оперативнее.
	defaultResourcesInterval = 30 * time.Second
	// envResourcesIntervalSeconds — переопределение периода в секундах.
	envResourcesIntervalSeconds = "RUNNER_RESOURCES_INTERVAL_SECONDS"
)

// RunResources периодически собирает снимок ресурсов и публикует его в
// package threat. Публикация не зависит от доступности backend: гейт
// блокировки активностей работает на локальном снимке.
func RunResources(ctx context.Context, workdir string) {
	// Docker API создаётся один раз: при неподдерживаемой конфигурации он nil.
	api := newDockerAPIClient()

	publishResourcesOnce(ctx, workdir, api)

	ticker := time.NewTicker(resourcesInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}

			publishResourcesOnce(ctx, workdir, api)
		}
	}
}

// publishResourcesOnce собирает и публикует один снимок ресурсов.
func publishResourcesOnce(ctx context.Context, workdir string, api *dockerAPIClient) {
	snapshot := threat.Publish(collectResources(ctx, workdir, api))

	if snapshot.Blocked {
		log.Printf("monitor: resources: уровень блокировки (%s)", snapshot.BlockReason)
	}
}

// resourcesInterval возвращает период сбора ресурсов с учётом окружения.
func resourcesInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envResourcesIntervalSeconds))
	if raw == "" {
		return defaultResourcesInterval
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return defaultResourcesInterval
	}

	return time.Duration(seconds) * time.Second
}

// collectResources собирает снимок ресурсов хоста: CPU, память, диски и
// занятое место Docker. Ошибки отдельных источников не прерывают сбор:
// соответствующая часть снимка остаётся нулевой.
func collectResources(ctx context.Context, workdir string, api *dockerAPIClient) threat.Snapshot {
	snapshot := threat.Snapshot{
		CollectedAt:   time.Now().Unix(),
		CPU:           collectCPU(),
		Memory:        collectMemory(),
		Disks:         collectDisks(ctx, workdir, api),
		UptimeSeconds: readUptimeSeconds(),
	}

	if api != nil {
		snapshot.Docker = collectDockerDiskUsage(ctx, api)
	}

	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("monitor: resources: hostname: %v", err)
	} else {
		snapshot.Hostname = hostname
	}

	return snapshot
}

// ─── CPU ────────────────────────────────────────────────────────────────────

type cpuTimes struct {
	total uint64
	idle  uint64
}

// collectCPU измеряет загрузку CPU по двум срезам /proc/stat и читает
// load average.
func collectCPU() threat.CpuStat {
	stat := threat.CpuStat{Cores: runtime.NumCPU()}
	stat.Load1, stat.Load5, stat.Load15 = readLoadAverage()

	first, err := readCPUTimes()
	if err != nil {
		log.Printf("monitor: resources: cpu: %v", err)
		return stat
	}

	// Между срезами даём процессору поработать, чтобы дельта была осмысленной.
	time.Sleep(cpuSampleInterval)

	second, err := readCPUTimes()
	if err != nil {
		log.Printf("monitor: resources: cpu: %v", err)
		return stat
	}

	if second.total <= first.total || second.idle < first.idle {
		return stat
	}

	deltaTotal := second.total - first.total
	deltaIdle := second.idle - first.idle

	stat.UsagePercent = float64(deltaTotal-deltaIdle) * 100 / float64(deltaTotal)

	return stat
}

// readCPUTimes читает суммарные и idle-тики процессора из /proc/stat.
func readCPUTimes() (cpuTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, fmt.Errorf("read /proc/stat: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			return cpuTimes{}, fmt.Errorf("некорректная строка cpu в /proc/stat")
		}

		var times cpuTimes
		for index, field := range fields[1:] {
			value, parseErr := strconv.ParseUint(field, 10, 64)
			if parseErr != nil {
				return cpuTimes{}, fmt.Errorf("parse /proc/stat: %w", parseErr)
			}

			times.total += value

			// idle (3) и iowait (4) в порядке полей /proc/stat.
			if index == 3 || index == 4 {
				times.idle += value
			}
		}

		return times, nil
	}

	return cpuTimes{}, fmt.Errorf("строка cpu в /proc/stat не найдена")
}

// readLoadAverage читает значения load average из /proc/loadavg.
func readLoadAverage() (load1, load5, load15 float64) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		log.Printf("monitor: resources: loadavg: %v", err)
		return 0, 0, 0
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0
	}

	load1, _ = strconv.ParseFloat(fields[0], 64)
	load5, _ = strconv.ParseFloat(fields[1], 64)
	load15, _ = strconv.ParseFloat(fields[2], 64)

	return load1, load5, load15
}

// readUptimeSeconds читает время работы хоста из /proc/uptime.
func readUptimeSeconds() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}

	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}

	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value < 0 {
		return 0
	}

	return int64(value)
}

// ─── память ─────────────────────────────────────────────────────────────────

// collectMemory читает потребление памяти из /proc/meminfo.
func collectMemory() threat.MemoryStat {
	values, err := readMemInfoValues()
	if err != nil {
		log.Printf("monitor: resources: meminfo: %v", err)
		return threat.MemoryStat{}
	}

	total := values["MemTotal"]
	available, found := values["MemAvailable"]
	if !found {
		available = values["MemFree"]
	}

	stat := threat.MemoryStat{
		TotalBytes:     total * 1024,
		AvailableBytes: available * 1024,
	}

	if stat.TotalBytes <= 0 {
		return threat.MemoryStat{}
	}

	stat.UsedBytes = stat.TotalBytes - stat.AvailableBytes
	if stat.UsedBytes < 0 {
		stat.UsedBytes = 0
	}

	stat.UsedPercent = float64(stat.UsedBytes) * 100 / float64(stat.TotalBytes)

	return stat
}

// readMemInfoValues разбирает /proc/meminfo в карту значений в килобайтах.
func readMemInfoValues() (map[string]int64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer file.Close()

	values := make(map[string]int64, 8)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, raw, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}

		fields := strings.Fields(raw)
		if len(fields) == 0 {
			continue
		}

		value, parseErr := strconv.ParseInt(fields[0], 10, 64)
		if parseErr != nil {
			continue
		}

		values[strings.TrimSpace(key)] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read /proc/meminfo: %w", err)
	}

	return values, nil
}

// ─── диски ──────────────────────────────────────────────────────────────────

// collectDisks собирает заполнение корня, рабочего каталога раннера и каталога
// Docker. Дубли по устройству схлопываются: для точки монтирования одного
// диска показывается первая найденная (корень приоритетнее).
func collectDisks(ctx context.Context, workdir string, api *dockerAPIClient) []threat.DiskStat {
	paths := []string{"/"}

	if workdir != "" {
		if absolute, err := filepath.Abs(workdir); err == nil {
			paths = append(paths, absolute)
		}
	}

	if api != nil {
		if info, err := api.serverInfo(ctx); err == nil && strings.TrimSpace(info.DockerRootDir) != "" {
			paths = append(paths, info.DockerRootDir)
		}
	}

	result := make([]threat.DiskStat, 0, len(paths))
	usedDevices := make(map[uint64]struct{}, len(paths))

	for _, path := range paths {
		stat, device, ok := diskUsage(path)
		if !ok {
			continue
		}

		if _, exists := usedDevices[device]; exists {
			continue
		}

		usedDevices[device] = struct{}{}
		result = append(result, stat)
	}

	return result
}

// diskUsage возвращает заполнение точки монтирования и идентификатор устройства.
func diskUsage(path string) (threat.DiskStat, uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		log.Printf("monitor: resources: stat %q: %v", path, err)
		return threat.DiskStat{}, 0, false
	}

	system, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return threat.DiskStat{}, 0, false
	}

	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		log.Printf("monitor: resources: statfs %q: %v", path, err)
		return threat.DiskStat{}, 0, false
	}

	blockSize := int64(fs.Bsize)
	if blockSize <= 0 {
		return threat.DiskStat{}, 0, false
	}

	total := int64(fs.Blocks) * blockSize
	available := int64(fs.Bavail) * blockSize
	used := total - available

	stat := threat.DiskStat{
		Path:           path,
		TotalBytes:     total,
		AvailableBytes: available,
		UsedBytes:      used,
	}

	if total > 0 {
		stat.UsedPercent = float64(used) * 100 / float64(total)
	}

	return stat, uint64(system.Dev), true
}

// ─── Docker ─────────────────────────────────────────────────────────────────

// collectDockerDiskUsage читает занятое место Docker через Docker Engine API.
func collectDockerDiskUsage(ctx context.Context, api *dockerAPIClient) *threat.DockerDiskUsage {
	usage, err := api.systemDiskUsage(ctx)
	if err != nil {
		log.Printf("monitor: resources: docker system df: %v", err)
		return nil
	}

	result := &threat.DockerDiskUsage{
		LayersBytes:     usage.LayersSize,
		BuildCacheBytes: usage.buildCacheSize(),
		ContainersBytes: usage.containersSize(),
		VolumesBytes:    usage.volumesSize(),
	}

	result.TotalBytes = result.LayersBytes + result.ContainersBytes + result.VolumesBytes + result.BuildCacheBytes
	// Кэш сборки — гарантированно освобождаемые данные; raw-слои без активных
	// образов Docker считает отдельно, здесь их не приписываем.
	result.ReclaimableBytes = result.BuildCacheBytes

	return result
}
