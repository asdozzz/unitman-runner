package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseComposeSize(t *testing.T) {
	cases := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"3g", 3 << 30, false},
		{"3072m", 3072 << 20, false},
		{"262144k", 262144 << 10, false},
		{"1073741824", 1073741824, false},
		{"512MB", 512 << 20, false},
		{"3221225472b", 3221225472, false},
		{"2GiB", 2 << 30, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-1g", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseComposeSize(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseComposeSize(%q) = %d, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseComposeSize(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parseComposeSize(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestHumanizeBytes(t *testing.T) {
	cases := []struct {
		size int64
		want string
	}{
		{0, "0B"},
		{1, "1B"},
		{1000, "1000B"},
		{1023, "1KiB"},
		{1 << 10, "1KiB"},
		{512 << 20, "512MiB"},
		{3 << 30, "3GiB"},
		{1536 << 20, "1.5GiB"},
		{(1 << 30) - 1, "1GiB"},
	}

	for _, tc := range cases {
		if got := humanizeBytes(tc.size); got != tc.want {
			t.Fatalf("humanizeBytes(%d) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

func TestUnitMemoryLimits(t *testing.T) {
	workdir := t.TempDir()
	writeEnv := func(project, unit, content string) {
		t.Helper()
		dir := filepath.Join(workdir, "projects", project, "units", unit)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o644); err != nil {
			t.Fatalf("write .env: %v", err)
		}
	}

	writeEnv("p1", "u1", "UNITMAN_UNIT_NAME=unit-1\nUNITMAN_PROJECT_NAME=proj\nUNITMAN_MEMORY_LIMIT=2g\n")
	// Пустой лимит — используется дефолт compose (3g).
	writeEnv("p1", "u2", "UNITMAN_UNIT_NAME=unit-2\nUNITMAN_PROJECT_NAME=proj\nUNITMAN_MEMORY_LIMIT=\n")
	// Нет имени проекта — запись пропускается.
	writeEnv("p1", "u3", "UNITMAN_UNIT_NAME=unit-3\n")

	limits := unitMemoryLimits(workdir)

	if len(limits) != 2 {
		t.Fatalf("len(limits) = %d, want 2: %#v", len(limits), limits)
	}
	if got := limits["unit-1.proj"]; got != 2<<30 {
		t.Fatalf("unit-1.proj limit = %d, want %d", got, int64(2<<30))
	}
	if got := limits["unit-2.proj"]; got != 3<<30 {
		t.Fatalf("unit-2.proj limit = %d, want %d", got, int64(3<<30))
	}
}

// statsHandler отдаёт inspect и stats для контейнера unit-1.proj.
func statsHandler(t *testing.T, hostConfigMemory, usage int64) http.Handler {
	t.Helper()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			_, _ = w.Write([]byte(`{"HostConfig":{"Memory":` + strconv.FormatInt(hostConfigMemory, 10) + `}}`))
		case strings.HasSuffix(r.URL.Path, "/stats"):
			_, _ = w.Write([]byte(`{"memory_stats":{"usage":` + strconv.FormatInt(usage, 10) + `,"limit":66571993088,"stats":{}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestEnrichDockerStatsUsesHostConfigLimit(t *testing.T) {
	api := newTestDockerAPIClient(t, statsHandler(t, 3<<30, 512<<20))

	raw := strings.Join([]string{
		`{"Container":"abc","Name":"unit-1.proj","CPUPerc":"1.50%","MemPerc":"0.86%","MemUsage":"512MiB / 62GiB","NetIO":"1kB / 2kB"}`,
		`{"Container":"def","Name":"other","CPUPerc":"0.00%","MemPerc":"0.00%","MemUsage":"1MiB / 62GiB","NetIO":"0B / 0B"}`,
	}, "\n")

	out := enrichDockerStats(context.Background(), raw, map[string]int64{"unit-1.proj": 2 << 30}, api)
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %q", len(lines), out)
	}

	var unit map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &unit); err != nil {
		t.Fatalf("unmarshal unit line: %v", err)
	}
	// HostConfig.Memory (3GiB) приоритетнее лимита из .env (2GiB).
	if unit["MemPerc"] != "16.67%" {
		t.Fatalf("MemPerc = %v, want 16.67%%", unit["MemPerc"])
	}
	if unit["MemUsage"] != "512MiB / 3GiB" {
		t.Fatalf("MemUsage = %v, want 512MiB / 3GiB", unit["MemUsage"])
	}
	// Прочие поля не потеряны.
	if unit["CPUPerc"] != "1.50%" || unit["NetIO"] != "1kB / 2kB" || unit["Container"] != "abc" {
		t.Fatalf("unexpected fields: %#v", unit)
	}

	// Строка не-юнита остаётся без изменений.
	if lines[1] != `{"Container":"def","Name":"other","CPUPerc":"0.00%","MemPerc":"0.00%","MemUsage":"1MiB / 62GiB","NetIO":"0B / 0B"}` {
		t.Fatalf("non-unit line was modified: %q", lines[1])
	}
}

func TestEnrichDockerStatsFallsBackToConfiguredLimit(t *testing.T) {
	// HostConfig.Memory = 0: рантайм лимит не применил, берём .env.
	api := newTestDockerAPIClient(t, statsHandler(t, 0, 512<<20))

	raw := `{"Container":"abc","Name":"unit-1.proj","CPUPerc":"0%","MemPerc":"0%","MemUsage":"512MiB / 62GiB","NetIO":"0B / 0B"}`

	out := enrichDockerStats(context.Background(), raw, map[string]int64{"unit-1.proj": 2 << 30}, api)

	var unit map[string]any
	if err := json.Unmarshal([]byte(out), &unit); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if unit["MemPerc"] != "25.00%" {
		t.Fatalf("MemPerc = %v, want 25.00%%", unit["MemPerc"])
	}
	if unit["MemUsage"] != "512MiB / 2GiB" {
		t.Fatalf("MemUsage = %v, want 512MiB / 2GiB", unit["MemUsage"])
	}
}

func TestEnrichDockerStatsDegrades(t *testing.T) {
	unitLine := `{"Container":"abc","Name":"unit-1.proj","CPUPerc":"0%","MemPerc":"0.86%","MemUsage":"512MiB / 62GiB","NetIO":"0B / 0B"}`
	limits := map[string]int64{"unit-1.proj": 3 << 30}

	t.Run("nil client", func(t *testing.T) {
		if got := enrichDockerStats(context.Background(), unitLine, limits, nil); got != unitLine {
			t.Fatalf("got %q, want unchanged line", got)
		}
	})

	t.Run("api error", func(t *testing.T) {
		api := newTestDockerAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))

		if got := enrichDockerStats(context.Background(), unitLine, limits, api); got != unitLine {
			t.Fatalf("got %q, want unchanged line", got)
		}
	})

	t.Run("malformed line", func(t *testing.T) {
		api := newTestDockerAPIClient(t, statsHandler(t, 3<<30, 512<<20))

		if got := enrichDockerStats(context.Background(), "not a json", limits, api); got != "not a json" {
			t.Fatalf("got %q, want unchanged line", got)
		}
	})

	t.Run("empty output", func(t *testing.T) {
		api := newTestDockerAPIClient(t, statsHandler(t, 3<<30, 512<<20))

		if got := enrichDockerStats(context.Background(), "", limits, api); got != "" {
			t.Fatalf("got %q, want empty string", got)
		}
	})
}

func TestFormatMemoryPercent(t *testing.T) {
	cases := []struct {
		usage int64
		limit int64
		want  string
	}{
		{0, 3 << 30, "0.00%"},
		{512 << 20, 3 << 30, "16.67%"},
		{2 << 30, 2 << 30, "100.00%"},
		{1, 0, "0.00%"},
	}

	for _, tc := range cases {
		if got := formatMemoryPercent(tc.usage, tc.limit); got != tc.want {
			t.Fatalf("formatMemoryPercent(%d, %d) = %q, want %q", tc.usage, tc.limit, got, tc.want)
		}
	}
}
