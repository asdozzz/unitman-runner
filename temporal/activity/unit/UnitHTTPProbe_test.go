package unit

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"runner/temporal/activity/unit/model"
)

func TestProbeHTTPPlainServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if !probeHTTP(serverHost(t, server.URL)) {
		t.Fatal("probeHTTP должен распознать plain HTTP сервер")
	}
}

func TestProbeHTTPTLSServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if !probeHTTP(serverHost(t, server.URL)) {
		t.Fatal("probeHTTP должен распознать HTTPS сервер с самоподписанным сертификатом")
	}
}

func TestProbeHTTPServerWithNonHTTPPayload(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := tlsServer.TLS.Certificates[0]
	tlsServer.Close()

	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{certificate},
	})
	if err != nil {
		t.Fatalf("создать TLS-сервер: %v", err)
	}
	defer listener.Close()

	serveConnections(listener, func(conn net.Conn) {
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			return
		}
		buf := make([]byte, 64)
		if _, err := conn.Read(buf); err != nil {
			return
		}
		if _, err := conn.Write([]byte("NOT-HTTP DATA\r\n")); err != nil {
			return
		}
	})

	if probeHTTP(listener.Addr().String()) {
		t.Fatal("probeHTTP не должен считать не-HTTP ответ HTTP-сервисом")
	}
}

func TestProbeHTTPServerWithSSHBanner(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("создать TCP-сервер: %v", err)
	}
	defer listener.Close()

	serveConnections(listener, func(conn net.Conn) {
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			return
		}
		buf := make([]byte, 128)
		if _, err := conn.Read(buf); err != nil {
			return
		}
		if _, err := conn.Write([]byte("SSH-2.0-test\r\n")); err != nil {
			return
		}
	})

	if probeHTTP(listener.Addr().String()) {
		t.Fatal("probeHTTP не должен считать SSH-баннер HTTP-сервисом")
	}
}

func TestProbeHTTPClosedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("создать TCP-сервер: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("закрыть TCP-сервер: %v", err)
	}

	if probeHTTP(addr) {
		t.Fatal("probeHTTP не должен находить HTTP на закрытом порту")
	}
}

func TestProbeHTTPHonoursReadTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("создать TCP-сервер: %v", err)
	}

	var mu sync.Mutex
	var connections []net.Conn
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
	})

	start := time.Now()
	if probeHTTP(listener.Addr().String()) {
		t.Fatal("probeHTTP не должен считать молчащий сервер HTTP-сервисом")
	}
	if elapsed := time.Since(start); elapsed >= 300*time.Millisecond {
		t.Fatalf("проверка молчащего сервера заняла %s, ожидалось меньше 300 мс", elapsed)
	}
}

func TestDetectHTTPPortsSkipsUDPAndUnpublished(t *testing.T) {
	ports := []model.UnitPort{
		{Container: "app", InternalPort: 80, ExternalPort: 8080, Type: "tcp"},
		{Container: "app", InternalPort: 53, ExternalPort: 5353, Type: "udp"},
		{Container: "app", InternalPort: 3000, ExternalPort: 0, Type: "tcp"},
		{Container: "api", InternalPort: 443, ExternalPort: 8443, Type: "tcp"},
	}

	var mu sync.Mutex
	checked := make(map[string]struct{})
	probe := func(addr string) bool {
		mu.Lock()
		defer mu.Unlock()
		checked[addr] = struct{}{}
		return addr == "10.0.0.5:8080"
	}

	got := detectHTTPPorts(ports, "10.0.0.5", probe)

	want := []model.UnitPort{
		{Container: "app", InternalPort: 80, ExternalPort: 8080, Type: "tcp", IsHTTP: true},
		{Container: "app", InternalPort: 53, ExternalPort: 5353, Type: "udp"},
		{Container: "app", InternalPort: 3000, ExternalPort: 0, Type: "tcp"},
		{Container: "api", InternalPort: 443, ExternalPort: 8443, Type: "tcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("неожиданный результат проверки: %#v", got)
	}

	if len(checked) != 2 {
		t.Fatalf("проверено %d адресов, ожидалось 2: %#v", len(checked), checked)
	}
	for _, addr := range []string{"10.0.0.5:8080", "10.0.0.5:8443"} {
		if _, ok := checked[addr]; !ok {
			t.Fatalf("адрес %s не проверялся", addr)
		}
	}
}

func TestDetectHTTPPortsCopiesInputWithoutTargets(t *testing.T) {
	ports := []model.UnitPort{
		{Container: "app", InternalPort: 53, ExternalPort: 5353, Type: "udp"},
	}

	got, err := DetectHTTPPorts("/nonexistent", "unit", "project", ports)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !reflect.DeepEqual(got, ports) {
		t.Fatalf("неожиданный результат: %#v", got)
	}

	got[0].Service = "changed"
	if ports[0].Service == "changed" {
		t.Fatal("результат должен быть копией входа")
	}
}

func TestDetectHTTPPortsRunsInParallel(t *testing.T) {
	ports := make([]model.UnitPort, 0, 10)
	for i := 0; i < 10; i++ {
		ports = append(ports, model.UnitPort{
			Container:    "app",
			InternalPort: 8000 + i,
			ExternalPort: 18000 + i,
			Type:         "tcp",
		})
	}

	probe := func(string) bool {
		time.Sleep(90 * time.Millisecond)
		return false
	}

	start := time.Now()
	got := detectHTTPPorts(ports, "127.0.0.1", probe)
	elapsed := time.Since(start)

	if len(got) != len(ports) {
		t.Fatalf("проверено %d портов, ожидалось %d", len(got), len(ports))
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("параллельная проверка заняла %s, ожидалось меньше 500 мс", elapsed)
	}
}

// serverHost извлекает host:port из URL httptest-сервера.
func serverHost(t *testing.T, rawURL string) string {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("разобрать URL %q: %v", rawURL, err)
	}

	return parsed.Host
}

// serveConnections обрабатывает входящие соединения в отдельных горутинах.
func serveConnections(listener net.Listener, handle func(net.Conn)) {
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handle(conn)
		}
	}()
}
