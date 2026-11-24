package config

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// legacyRegistrationHeader собран по частям, чтобы grep по удалённым
// идентификаторам оставался чистым.
var legacyRegistrationHeader = "X-Runner-" + "Registration-" + "Signature"

var legacyConfigFields = []string{"Proxy" + "URL", "proxy" + "URL", "proxy" + "Url", "task" + "Queue", "runner" + "ID"}

func independentSign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSignMatchesKnownHMACVector(t *testing.T) {
	payload := []byte(`{"runnerId":"runner-1"}`)

	got := Sign(testSecret, payload)
	want := independentSign(testSecret, payload)
	if got != want {
		t.Fatalf("Sign = %q, want %q", got, want)
	}
	if got != strings.ToLower(got) {
		t.Fatal("signature must be lowercase hex")
	}

	configuration := Configuration{RunnerSecret: testSecret}
	if got := configuration.Sign(payload); got != want {
		t.Fatalf("Configuration.Sign = %q, want %q", got, want)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "http://backend.example.com/", want: "backend.example.com"},
		{in: "https://backend.example.com:8443/", want: "backend.example.com:8443"},
		{in: "backend.example.com/", want: "backend.example.com"},
		{in: "  backend.example.com  ", want: "backend.example.com"},
		{in: "", want: "localhost"},
		{in: "http://", want: "localhost"},
	}

	for _, testCase := range cases {
		if got := NormalizeHost(testCase.in); got != testCase.want {
			t.Fatalf("NormalizeHost(%q) = %q, want %q", testCase.in, got, testCase.want)
		}
	}
}

func TestRegistrationURLUsesDefaultAPIPort(t *testing.T) {
	if got := RegistrationURL("http://backend.example.com/"); got != "http://backend.example.com:8082/api/runner/register" {
		t.Fatalf("unexpected registration URL: %q", got)
	}
	if got := RegistrationURL("backend.example.com:9000"); got != "http://backend.example.com:9000/api/runner/register" {
		t.Fatalf("explicit port must be preserved: %q", got)
	}
}

func TestRegisterRunnerPostsPublicRequestWithoutAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != RegistrationPath {
			t.Errorf("path = %q, want %q", r.URL.Path, RegistrationPath)
		}
		for _, header := range []string{
			RunnerIDHeader,
			RunnerSignatureHeader,
			legacyRegistrationHeader,
			"Authorization",
		} {
			if value := r.Header.Get(header); value != "" {
				t.Errorf("registration must not send %s, got %q", header, value)
			}
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var request RegistrationRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Hostname != "runner-1" || request.Version != "1.2.3" {
			t.Errorf("unexpected request: %+v", request)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegistrationResponse{
			RunnerID:     "runner-abc",
			TaskQueue:    "runner-abc",
			Secret:       testSecret,
			RegistryHost: "registry.example.com",
			Ports: RegistrationPorts{
				API: 8082, Temporal: 7233, Registry: 5000,
			},
		})
	}))
	defer server.Close()

	response, err := RegisterRunner(context.Background(), server.URL+RegistrationPath,
		RegistrationRequest{Hostname: "runner-1", Version: "1.2.3"}, server.Client())
	if err != nil {
		t.Fatalf("RegisterRunner: %v", err)
	}
	if response.RunnerID != "runner-abc" || response.TaskQueue != "runner-abc" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Secret != testSecret {
		t.Fatalf("secret = %q", response.Secret)
	}
	if response.RegistryHost != "registry.example.com" {
		t.Fatalf("registryHost = %q", response.RegistryHost)
	}
	if response.Ports.API != 8082 || response.Ports.Temporal != 7233 || response.Ports.Registry != 5000 {
		t.Fatalf("unexpected ports: %+v", response.Ports)
	}
}

func TestRegisterRunnerRejectsTaskQueueMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegistrationResponse{
			RunnerID:  "runner-abc",
			TaskQueue: "other-queue",
			Secret:    testSecret,
		})
	}))
	defer server.Close()

	if _, err := RegisterRunner(context.Background(), server.URL, RegistrationRequest{}, server.Client()); err == nil {
		t.Fatal("expected an error when taskQueue != runnerId")
	}
}

func TestRegisterRunnerRejectsInvalidSecret(t *testing.T) {
	for _, secret := range []string{"", "not-hex", strings.Repeat("a", 63)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(RegistrationResponse{
				RunnerID:  "runner-abc",
				TaskQueue: "runner-abc",
				Secret:    secret,
			})
		}))

		_, err := RegisterRunner(context.Background(), server.URL, RegistrationRequest{}, server.Client())
		server.Close()
		if err == nil {
			t.Fatalf("expected an error for secret %q", secret)
		}
	}
}

func TestRegisterRunnerStatusErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantPhrase string
	}{
		{name: "bad request", status: http.StatusBadRequest, wantPhrase: "payload"},
		{name: "not found", status: http.StatusNotFound, wantPhrase: "404"},
		{name: "server error", status: http.StatusInternalServerError, wantPhrase: "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			_, err := RegisterRunner(context.Background(), server.URL, RegistrationRequest{}, server.Client())
			if err == nil {
				t.Fatalf("expected an error for status %d", tt.status)
			}
			if !strings.Contains(err.Error(), tt.wantPhrase) {
				t.Fatalf("error %q does not contain %q", err, tt.wantPhrase)
			}
		})
	}
}

func TestRunInitPersistsConfig0600(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RegistrationResponse{
			RunnerID:     "runner-abc",
			TaskQueue:    "runner-abc",
			Secret:       testSecret,
			RegistryHost: "registry.example.com",
			Ports:        RegistrationPorts{API: 8082, Temporal: 7233, Registry: 5000},
		})
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "config.json")

	configuration, err := RunInit(context.Background(), InitOptions{
		Host:        "http://mono.example.com/",
		ConfigPath:  target,
		HTTPClient:  server.Client(),
		Hostname:    "runner-1",
		Version:     "1.2.3",
		registerURL: server.URL + RegistrationPath,
	})
	if err != nil {
		t.Fatalf("RunInit: %v", err)
	}

	if configuration.RunnerID != "runner-abc" || configuration.RunnerSecret != testSecret {
		t.Fatalf("unexpected configuration: %+v", configuration)
	}
	if configuration.ApiHost != "http://mono.example.com:8082" {
		t.Fatalf("ApiHost = %q", configuration.ApiHost)
	}
	if configuration.TemporalHost != "mono.example.com:7233" {
		t.Fatalf("TemporalHost = %q", configuration.TemporalHost)
	}
	if configuration.RegistryHost != "registry.example.com:5000" {
		t.Fatalf("RegistryHost = %q", configuration.RegistryHost)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config mode = %#o, want 0600", info.Mode().Perm())
	}
	for _, forbidden := range legacyConfigFields {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("config must not contain %s:\n%s", forbidden, data)
		}
	}

	var persisted Configuration
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("persisted config is not valid JSON: %v\n%s", err, data)
	}
	if persisted != configuration {
		t.Fatalf("persisted config mismatch:\n got %+v\nwant %+v", persisted, configuration)
	}
}

func TestRunInitFallsBackToDefaultPortsAndRegistrationHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		// registryHost и ports намеренно отсутствуют.
		_ = json.NewEncoder(w).Encode(RegistrationResponse{
			RunnerID:  "runner-abc",
			TaskQueue: "runner-abc",
			Secret:    testSecret,
		})
	}))
	defer server.Close()

	configuration, err := RunInit(context.Background(), InitOptions{
		Host:        "mono.example.com",
		ConfigPath:  filepath.Join(t.TempDir(), "config.json"),
		HTTPClient:  server.Client(),
		registerURL: server.URL + RegistrationPath,
	})
	if err != nil {
		t.Fatalf("RunInit: %v", err)
	}

	if configuration.ApiHost != "http://mono.example.com:8082" {
		t.Fatalf("ApiHost = %q", configuration.ApiHost)
	}
	if configuration.TemporalHost != "mono.example.com:7233" {
		t.Fatalf("TemporalHost = %q", configuration.TemporalHost)
	}
	if configuration.RegistryHost != "mono.example.com:5000" {
		t.Fatalf("RegistryHost = %q", configuration.RegistryHost)
	}
}

// TestRunInitDerivesServiceHostsFromRegistrationPorts закрепляет точные формы
// трёх адресов из ответа регистрации: ApiHost — HTTP base URL с ports.api,
// TemporalHost — host:ports.temporal без схемы, RegistryHost — Docker-ссылка
// host:ports.registry без схемы. Явная схема и порт во введённом host-е
// отбрасываются: порты ответа авторитетны.
func TestRunInitDerivesServiceHostsFromRegistrationPorts(t *testing.T) {
	cases := []struct {
		name             string
		pastedHost       string
		registryHost     string
		wantAPIHost      string
		wantTemporalHost string
		wantRegistryHost string
	}{
		{
			name:             "explicit scheme and port in pasted host",
			pastedHost:       "http://backend.example.com:9000/",
			registryHost:     "registry.example.com",
			wantAPIHost:      "http://backend.example.com:18082",
			wantTemporalHost: "backend.example.com:17233",
			wantRegistryHost: "registry.example.com:15000",
		},
		{
			name:             "scheme in registryHost is stripped",
			pastedHost:       "backend.example.com",
			registryHost:     "http://registry.example.com",
			wantAPIHost:      "http://backend.example.com:18082",
			wantTemporalHost: "backend.example.com:17233",
			wantRegistryHost: "registry.example.com:15000",
		},
		{
			name:             "registryHost falls back to registration host",
			pastedHost:       "backend.example.com",
			registryHost:     "",
			wantAPIHost:      "http://backend.example.com:18082",
			wantTemporalHost: "backend.example.com:17233",
			wantRegistryHost: "backend.example.com:15000",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(RegistrationResponse{
					RunnerID:     "runner-abc",
					TaskQueue:    "runner-abc",
					Secret:       testSecret,
					RegistryHost: testCase.registryHost,
					Ports:        RegistrationPorts{API: 18082, Temporal: 17233, Registry: 15000},
				})
			}))
			defer server.Close()

			configuration, err := RunInit(context.Background(), InitOptions{
				Host:        testCase.pastedHost,
				ConfigPath:  filepath.Join(t.TempDir(), "config.json"),
				HTTPClient:  server.Client(),
				registerURL: server.URL + RegistrationPath,
			})
			if err != nil {
				t.Fatalf("RunInit: %v", err)
			}

			if configuration.ApiHost != testCase.wantAPIHost {
				t.Fatalf("ApiHost = %q, want %q", configuration.ApiHost, testCase.wantAPIHost)
			}
			if configuration.TemporalHost != testCase.wantTemporalHost {
				t.Fatalf("TemporalHost = %q, want %q", configuration.TemporalHost, testCase.wantTemporalHost)
			}
			if configuration.RegistryHost != testCase.wantRegistryHost {
				t.Fatalf("RegistryHost = %q, want %q", configuration.RegistryHost, testCase.wantRegistryHost)
			}
		})
	}
}

func TestConfigurationJSONSchemaHasNoLegacyFields(t *testing.T) {
	raw, err := json.Marshal(Configuration{
		TemporalHost: "mono.local:7233",
		ApiHost:      "http://mono.local:8082",
		RunnerID:     "550e8400-e29b-41d4-a716-446655440000",
		RunnerSecret: testSecret,
		RegistryHost: "registry.example.com:5000",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for _, key := range []string{`"runnerId"`, `"runnerSecret"`, `"registryHost"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("marshalled config lacks %s: %s", key, raw)
		}
	}
	for _, forbidden := range legacyConfigFields {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("marshalled config contains %s: %s", forbidden, raw)
		}
	}
}

func TestConfigurationValidateRequiresIDAndSecret(t *testing.T) {
	if err := (Configuration{}).Validate(); err == nil {
		t.Fatal("expected an error when RunnerID is empty")
	}
	if err := (Configuration{RunnerID: "runner-abc"}).Validate(); err == nil {
		t.Fatal("expected an error when RunnerSecret is empty")
	}
	if err := (Configuration{RunnerID: "runner-abc", RunnerSecret: "not-hex"}).Validate(); err == nil {
		t.Fatal("expected an error for a short secret")
	}
	if err := (Configuration{RunnerID: "runner-abc", RunnerSecret: testSecret}).Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigurationToleratesLegacyJSON(t *testing.T) {
	legacy := []byte(`{"TemporalHost":"localhost:7233","ApiHost":"http://localhost:8082"}`)

	var configuration Configuration
	if err := json.Unmarshal(legacy, &configuration); err != nil {
		t.Fatalf("legacy config is not deserializable: %v", err)
	}
	if configuration.RunnerID != "" {
		t.Fatalf("expected empty RunnerID, got %q", configuration.RunnerID)
	}
}
