package unit

import (
	"reflect"
	"testing"

	"runner/temporal/activity/unit/model"
)

func TestParseDockerInspectPortsPublishedTCPDeduplicatesIPv4AndIPv6(t *testing.T) {
	raw := []byte(`[
		{
			"Name": "/app-web-1",
			"Config": {
				"Labels": {"com.docker.compose.service": "web"},
				"ExposedPorts": {"8080/tcp": {}}
			},
			"NetworkSettings": {
				"Ports": {
					"8080/tcp": [
						{"HostIp": "0.0.0.0", "HostPort": "8080"},
						{"HostIp": "::", "HostPort": "8080"}
					]
				}
			}
		}
	]`)

	got, err := parseDockerInspectPorts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []model.UnitPort{
		{
			Container:    "app-web-1",
			Service:      "web",
			InternalPort: 8080,
			ExternalPort: 8080,
			Type:         "tcp",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected ports: %#v", got)
	}
}

func TestParseDockerInspectPortsPublishedUDP(t *testing.T) {
	raw := []byte(`[
		{
			"Name": "/dns",
			"Config": {
				"Labels": {"com.docker.compose.service": "dns"},
				"ExposedPorts": {"53/udp": {}}
			},
			"NetworkSettings": {
				"Ports": {
					"53/udp": [
						{"HostIp": "0.0.0.0", "HostPort": "5353"}
					]
				}
			}
		}
	]`)

	got, err := parseDockerInspectPorts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []model.UnitPort{
		{
			Container:    "dns",
			Service:      "dns",
			InternalPort: 53,
			ExternalPort: 5353,
			Type:         "udp",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected ports: %#v", got)
	}
}

func TestParseDockerInspectPortsExposedWithoutPublication(t *testing.T) {
	raw := []byte(`[
		{
			"Name": "/app",
			"Config": {
				"Labels": {"com.docker.compose.service": "app"},
				"ExposedPorts": {"3000/tcp": {}}
			},
			"NetworkSettings": {
				"Ports": {
					"3000/tcp": []
				}
			}
		}
	]`)

	got, err := parseDockerInspectPorts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []model.UnitPort{
		{
			Container:    "app",
			Service:      "app",
			InternalPort: 3000,
			ExternalPort: 0,
			Type:         "tcp",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected ports: %#v", got)
	}
}

func TestParseDockerInspectPortsMultipleContainersAndServices(t *testing.T) {
	raw := []byte(`[
		{
			"Name": "/z-api-1",
			"Config": {
				"Labels": {"com.docker.compose.service": "api"},
				"ExposedPorts": {"8080/tcp": {}}
			},
			"NetworkSettings": {
				"Ports": {
					"8080/tcp": [
						{"HostIp": "0.0.0.0", "HostPort": "18080"}
					]
				}
			}
		},
		{
			"Name": "/a-db-1",
			"Config": {
				"Labels": {"com.docker.compose.service": "db"},
				"ExposedPorts": {"5432/tcp": {}}
			},
			"NetworkSettings": {
				"Ports": {
					"5432/tcp": [
						{"HostIp": "0.0.0.0", "HostPort": "15432"}
					]
				}
			}
		}
	]`)

	got, err := parseDockerInspectPorts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []model.UnitPort{
		{
			Container:    "z-api-1",
			Service:      "api",
			InternalPort: 8080,
			ExternalPort: 18080,
			Type:         "tcp",
		},
		{
			Container:    "a-db-1",
			Service:      "db",
			InternalPort: 5432,
			ExternalPort: 15432,
			Type:         "tcp",
		},
	}
	// Порядок задаётся сортировкой по Service, Container, InternalPort,
	// ExternalPort, Type: api идёт перед db.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected ports: %#v", got)
	}
}

func TestParseDockerInspectPortsContainerWithoutComposeLabels(t *testing.T) {
	raw := []byte(`[
		{
			"Name": "/standalone",
			"Config": {
				"Labels": {},
				"ExposedPorts": {"8080/tcp": {}}
			},
			"NetworkSettings": {
				"Ports": {
					"8080/tcp": [
						{"HostIp": "0.0.0.0", "HostPort": "8080"}
					]
				}
			}
		}
	]`)

	got, err := parseDockerInspectPorts(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []model.UnitPort{
		{
			Container:    "standalone",
			Service:      "",
			InternalPort: 8080,
			ExternalPort: 8080,
			Type:         "tcp",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected ports: %#v", got)
	}
}

func TestParseDockerInspectPortsEmptyInput(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(""), []byte("   \n\t")} {
		got, err := parseDockerInspectPorts(raw)
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", raw, err)
		}
		if got == nil {
			t.Fatalf("expected non-nil empty slice for %q", raw)
		}
		if len(got) != 0 {
			t.Fatalf("expected no ports for %q, got %#v", raw, got)
		}
	}
}

func TestParseDockerInspectPortsInvalidJSON(t *testing.T) {
	if _, err := parseDockerInspectPorts([]byte(`{"Name":`)); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}
