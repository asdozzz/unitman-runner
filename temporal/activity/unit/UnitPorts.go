package unit

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"runner/temporal/activity/unit/model"
)

const (
	// collectUnitPortsCommand собирает JSON-описание всех запущенных
	// контейнеров внутри Docker/Podman юнита.
	collectUnitPortsCommand = `ids=$(docker ps -q --no-trunc); if [ -n "$ids" ]; then docker inspect $ids; fi`

	// unitPortsStepCommand — короткое имя шага сбора портов в Steps.
	unitPortsStepCommand = "Сбор портов юнита"

	composeServiceLabel = "com.docker.compose.service"
)

type dockerInspectContainer struct {
	Name   string `json:"Name"`
	Config struct {
		Labels       map[string]string   `json:"Labels"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
	} `json:"Config"`
	NetworkSettings struct {
		Ports map[string][]dockerInspectPortBinding `json:"Ports"`
	} `json:"NetworkSettings"`
}

type dockerInspectPortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type unitPortIdentity struct {
	internalPort int
	portType     string
}

// CollectUnitPorts выполняет docker inspect внутри юнита и возвращает
// порты всех запущенных там контейнеров.
func CollectUnitPorts(unitPath string) ([]model.UnitPort, error) {
	raw, err := ExecInUnitContainer(unitPath, collectUnitPortsCommand)
	if err != nil {
		return nil, fmt.Errorf("собрать порты контейнеров юнита: %w", err)
	}

	return parseDockerInspectPorts([]byte(raw))
}

// parseDockerInspectPorts разбирает вывод docker inspect в список портов.
// Результат отсортирован по Service, Container, InternalPort, ExternalPort, Type.
func parseDockerInspectPorts(raw []byte) ([]model.UnitPort, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return []model.UnitPort{}, nil
	}

	var containers []dockerInspectContainer
	if err := json.Unmarshal(raw, &containers); err != nil {
		return nil, fmt.Errorf("разобрать вывод docker inspect: %w", err)
	}

	ports := make([]model.UnitPort, 0)
	seen := make(map[model.UnitPort]struct{})

	for _, container := range containers {
		name := strings.TrimPrefix(container.Name, "/")
		service := container.Config.Labels[composeServiceLabel]

		published := make(map[unitPortIdentity]map[int]struct{})
		known := make(map[unitPortIdentity]struct{})

		for key, bindings := range container.NetworkSettings.Ports {
			internalPort, portType, ok := parseDockerPortKey(key)
			if !ok {
				continue
			}

			identity := unitPortIdentity{internalPort: internalPort, portType: portType}
			known[identity] = struct{}{}

			for _, binding := range bindings {
				hostPort, err := strconv.Atoi(strings.TrimSpace(binding.HostPort))
				if err != nil || hostPort <= 0 {
					continue
				}
				if published[identity] == nil {
					published[identity] = make(map[int]struct{})
				}
				// Одинаковый порт на 0.0.0.0 и :: схлопывается в одну запись.
				published[identity][hostPort] = struct{}{}
			}
		}

		for key := range container.Config.ExposedPorts {
			internalPort, portType, ok := parseDockerPortKey(key)
			if !ok {
				continue
			}
			known[unitPortIdentity{internalPort: internalPort, portType: portType}] = struct{}{}
		}

		for identity := range known {
			externalPorts := published[identity]
			if len(externalPorts) == 0 {
				ports = appendUnitPort(ports, seen, model.UnitPort{
					Container:    name,
					Service:      service,
					InternalPort: identity.internalPort,
					ExternalPort: 0,
					Type:         identity.portType,
				})
				continue
			}

			for externalPort := range externalPorts {
				ports = appendUnitPort(ports, seen, model.UnitPort{
					Container:    name,
					Service:      service,
					InternalPort: identity.internalPort,
					ExternalPort: externalPort,
					Type:         identity.portType,
				})
			}
		}
	}

	sort.Slice(ports, func(i, j int) bool {
		return lessUnitPort(ports[i], ports[j])
	})

	return ports, nil
}

func appendUnitPort(ports []model.UnitPort, seen map[model.UnitPort]struct{}, port model.UnitPort) []model.UnitPort {
	if _, ok := seen[port]; ok {
		return ports
	}
	seen[port] = struct{}{}

	return append(ports, port)
}

func lessUnitPort(a, b model.UnitPort) bool {
	if a.Service != b.Service {
		return a.Service < b.Service
	}
	if a.Container != b.Container {
		return a.Container < b.Container
	}
	if a.InternalPort != b.InternalPort {
		return a.InternalPort < b.InternalPort
	}
	if a.ExternalPort != b.ExternalPort {
		return a.ExternalPort < b.ExternalPort
	}

	return a.Type < b.Type
}

func parseDockerPortKey(key string) (int, string, bool) {
	portPart, typePart, found := strings.Cut(key, "/")
	if !found {
		return 0, "", false
	}

	internalPort, err := strconv.Atoi(strings.TrimSpace(portPart))
	if err != nil || internalPort <= 0 || internalPort > 65535 {
		return 0, "", false
	}

	portType := strings.ToLower(strings.TrimSpace(typePart))
	if portType != "tcp" && portType != "udp" {
		return 0, "", false
	}

	return internalPort, portType, true
}
