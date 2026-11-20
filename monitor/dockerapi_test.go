package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestDockerAPIClient создаёт клиент, направленный на тестовый HTTP-сервер.
func newTestDockerAPIClient(t *testing.T, handler http.Handler) *dockerAPIClient {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &dockerAPIClient{httpClient: server.Client(), baseURL: server.URL}
}

func TestDockerAPIClientInspectContainer(t *testing.T) {
	api := newTestDockerAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/unit-1.proj/json" {
			t.Errorf("path = %q, want /containers/unit-1.proj/json", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"HostConfig":{"Memory":3221225472}}`))
	}))

	inspect, err := api.inspectContainer(context.Background(), "unit-1.proj")
	if err != nil {
		t.Fatalf("inspectContainer: %v", err)
	}
	if inspect.HostConfig.Memory != 3221225472 {
		t.Fatalf("HostConfig.Memory = %d, want 3221225472", inspect.HostConfig.Memory)
	}
}

func TestDockerAPIClientContainerStats(t *testing.T) {
	api := newTestDockerAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/unit-1.proj/stats" {
			t.Errorf("path = %q, want /containers/unit-1.proj/stats", r.URL.Path)
		}
		if r.URL.Query().Get("stream") != "false" {
			t.Errorf("stream = %q, want false", r.URL.Query().Get("stream"))
		}
		_, _ = w.Write([]byte(`{"memory_stats":{"usage":536870912,"limit":66571993088,"stats":{"inactive_file":134217728}}}`))
	}))

	sample, err := api.containerStatsSample(context.Background(), "unit-1.proj")
	if err != nil {
		t.Fatalf("containerStatsSample: %v", err)
	}
	if sample.MemoryStats.Usage != 536870912 {
		t.Fatalf("usage = %d, want 536870912", sample.MemoryStats.Usage)
	}
	// 512MiB - 128MiB = 384MiB
	if got := dockerMemUsage(sample); got != 402653184 {
		t.Fatalf("dockerMemUsage = %d, want 402653184", got)
	}
}

func TestDockerAPIClientErrors(t *testing.T) {
	api := newTestDockerAPIClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	if _, err := api.inspectContainer(context.Background(), "unit"); err == nil {
		t.Fatal("inspectContainer: expected error")
	}
	if _, err := api.containerStatsSample(context.Background(), "unit"); err == nil {
		t.Fatal("containerStatsSample: expected error")
	}
}

func TestDockerMemUsage(t *testing.T) {
	newSample := func(usage int64, stats map[string]int64) *containerStats {
		sample := &containerStats{}
		sample.MemoryStats.Usage = usage
		sample.MemoryStats.Stats = stats
		return sample
	}

	cases := []struct {
		name   string
		sample *containerStats
		want   int64
	}{
		{"nil", nil, 0},
		{"without cache", newSample(1000, nil), 1000},
		{"cgroup v1", newSample(1000, map[string]int64{"total_inactive_file": 400}), 600},
		{"cgroup v2", newSample(1000, map[string]int64{"inactive_file": 250}), 750},
		{"v1 wins over v2", newSample(1000, map[string]int64{"total_inactive_file": 400, "inactive_file": 250}), 600},
		{"cache larger than usage is ignored", newSample(1000, map[string]int64{"inactive_file": 2000}), 1000},
		{"zero usage", newSample(0, map[string]int64{"inactive_file": 10}), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dockerMemUsage(tc.sample); got != tc.want {
				t.Fatalf("dockerMemUsage = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNewDockerAPIClientFromEnv(t *testing.T) {
	t.Run("tcp", func(t *testing.T) {
		t.Setenv(dockerHostEnvVar, "tcp://127.0.0.1:2375")

		api := newDockerAPIClient()
		if api == nil {
			t.Fatal("newDockerAPIClient returned nil for tcp host")
		}
		if api.baseURL != "http://127.0.0.1:2375" {
			t.Fatalf("baseURL = %q, want http://127.0.0.1:2375", api.baseURL)
		}
	})

	t.Run("unix", func(t *testing.T) {
		t.Setenv(dockerHostEnvVar, "unix:///tmp/unitman-test.sock")

		api := newDockerAPIClient()
		if api == nil {
			t.Fatal("newDockerAPIClient returned nil for unix host")
		}
		if api.baseURL != dockerAPIPseudoHost {
			t.Fatalf("baseURL = %q, want %q", api.baseURL, dockerAPIPseudoHost)
		}
	})

	t.Run("default", func(t *testing.T) {
		t.Setenv(dockerHostEnvVar, "")

		api := newDockerAPIClient()
		if api == nil {
			t.Fatal("newDockerAPIClient returned nil for default host")
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		t.Setenv(dockerHostEnvVar, "npipe:////./pipe/docker_engine")

		if api := newDockerAPIClient(); api != nil {
			t.Fatalf("newDockerAPIClient = %#v, want nil for unsupported host", api)
		}
	})
}
