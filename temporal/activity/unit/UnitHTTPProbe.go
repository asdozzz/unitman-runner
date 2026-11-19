package unit

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"runner/temporal/activity/unit/model"
	"runner/temporal/utils"
)

const (
	// httpProbeDialTimeout ограничивает TCP connect и TLS handshake.
	httpProbeDialTimeout = 50 * time.Millisecond
	// httpProbeReadTimeout ограничивает чтение статусной строки ответа.
	httpProbeReadTimeout = 100 * time.Millisecond
	// httpProbeMaxConcurrency ограничивает число одновременных проверок.
	httpProbeMaxConcurrency = 64
	// unitNetworkName — сеть, в которой контейнер юнита доступен с хоста раннера.
	unitNetworkName = "unitman_default"
	// unitHTTPProbeStepCommand — короткое имя шага проверки HTTP-портов в Steps.
	unitHTTPProbeStepCommand = "Проверка HTTP-портов"

	httpProbeRequest     = "GET / HTTP/1.0\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	httpProbeMaxResponse = 512
	httpProbeStatusLine  = "HTTP/"
)

// DetectHTTPPorts проверяет опубликованные TCP-порты юнита по HTTP и HTTPS.
// Адреса проверяются параллельно, без ретраев и с минимальными таймаутами.
// Если проверять нечего, docker inspect не выполняется и возвращается копия входа.
func DetectHTTPPorts(workingDir, unitName, projectName string, ports []model.UnitPort) ([]model.UnitPort, error) {
	if !hasHTTPProbeTargets(ports) {
		return cloneUnitPorts(ports), nil
	}

	host, err := unitContainerIP(workingDir, unitName, projectName)
	if err != nil {
		return nil, err
	}

	return detectHTTPPorts(ports, host, probeHTTP), nil
}

// httpPortsSummary возвращает краткую сводку проверки без адресов и сырых данных.
func httpPortsSummary(ports []model.UnitPort) string {
	checked, httpCount := 0, 0
	for _, port := range ports {
		if shouldProbeHTTP(port) {
			checked++
		}
		if port.IsHTTP {
			httpCount++
		}
	}

	return fmt.Sprintf("HTTP: %d из %d", httpCount, checked)
}

// unitContainerIP возвращает IP контейнера юнита в сети unitNetworkName.
func unitContainerIP(workingDir, unitName, projectName string) (string, error) {
	container := unitName + "." + projectName
	format := fmt.Sprintf(`{{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{end}}`, unitNetworkName)
	args := []string{"docker", "inspect", "--format", format, container}

	out, err := utils.ExecCommand(workingDir, args)
	if err != nil {
		return "", fmt.Errorf("определить IP контейнера юнита %q в сети %s: %w", container, unitNetworkName, err)
	}

	ip := strings.TrimSpace(out)
	if ip == "" {
		return "", fmt.Errorf("контейнер юнита %q не имеет IP в сети %s", container, unitNetworkName)
	}

	return ip, nil
}

// detectHTTPPorts параллельно проверяет подходящие порты и заполняет IsHTTP.
// probe инъецируется для тестируемости; параметр host — IP контейнера юнита.
func detectHTTPPorts(ports []model.UnitPort, host string, probe func(string) bool) []model.UnitPort {
	result := cloneUnitPorts(ports)

	sem := make(chan struct{}, httpProbeMaxConcurrency)
	var wg sync.WaitGroup

	for i := range result {
		if !shouldProbeHTTP(result[i]) {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(index int) {
			defer wg.Done()
			defer func() { <-sem }()

			addr := net.JoinHostPort(host, strconv.Itoa(result[index].ExternalPort))
			if probe(addr) {
				result[index].IsHTTP = true
			}
		}(i)
	}

	wg.Wait()

	return result
}

// shouldProbeHTTP сообщает, нужно ли проверять порт: только опубликованные TCP-порты.
func shouldProbeHTTP(port model.UnitPort) bool {
	return port.Type == "tcp" && port.ExternalPort > 0
}

func hasHTTPProbeTargets(ports []model.UnitPort) bool {
	for _, port := range ports {
		if shouldProbeHTTP(port) {
			return true
		}
	}

	return false
}

func cloneUnitPorts(ports []model.UnitPort) []model.UnitPort {
	result := make([]model.UnitPort, len(ports))
	copy(result, ports)

	return result
}

// probeHTTP параллельно проверяет plain HTTP и HTTPS. Достаточно одного успеха.
func probeHTTP(addr string) bool {
	var wg sync.WaitGroup
	wg.Add(2)

	var httpOK, httpsOK bool
	go func() {
		defer wg.Done()
		httpOK = probePlainHTTP(addr)
	}()
	go func() {
		defer wg.Done()
		httpsOK = probeHTTPS(addr)
	}()
	wg.Wait()

	return httpOK || httpsOK
}

func probePlainHTTP(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, httpProbeDialTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	return exchangeHTTP(conn)
}

func probeHTTPS(addr string) bool {
	dialer := &net.Dialer{Timeout: httpProbeDialTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		// У сервисов юнита могут быть самоподписанные сертификаты,
		// поэтому проверку цепочки отключаем: важен сам факт HTTP-ответа.
		InsecureSkipVerify: true, //nolint:gosec // самоподписанные сертификаты сервисов юнита
		NextProtos:         []string{"http/1.1"},
	})
	if err != nil {
		return false
	}
	defer conn.Close()

	return exchangeHTTP(conn)
}

// exchangeHTTP отправляет запрос и проверяет префикс статусной строки.
func exchangeHTTP(conn net.Conn) bool {
	if err := conn.SetDeadline(time.Now().Add(httpProbeReadTimeout)); err != nil {
		return false
	}

	if _, err := io.WriteString(conn, httpProbeRequest); err != nil {
		return false
	}

	buf := make([]byte, httpProbeMaxResponse)
	n, err := conn.Read(buf)
	if err != nil && n == 0 {
		return false
	}

	return n > 0 && hasHTTPStatusPrefix(buf[:n])
}

func hasHTTPStatusPrefix(data []byte) bool {
	return strings.HasPrefix(strings.ToUpper(string(data)), httpProbeStatusLine)
}
