package unit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"runner/temporal/activity/unit/model"
)

const saveStepsTestSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func signSaveSteps(secret, responseID, stepsContent string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(responseID))
	_, _ = mac.Write([]byte(stepsContent))

	return hex.EncodeToString(mac.Sum(nil))
}

// TestSaveStepsToMonoSignsResponseIDAndStepsContent закрепляет контракт
// unit-пути saveSteps: подписывается responseId+stepsContent (не JSON-тело),
// а X-Runner-Id равен runnerId из config.json.
func TestSaveStepsToMonoSignsResponseIDAndStepsContent(t *testing.T) {
	type captured struct {
		method      string
		path        string
		runnerID    string
		signature   string
		responseID  string
		content     string
		contentType string
	}

	capturedCh := make(chan captured, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}

		var request SaveStepsRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode saveSteps request: %v", err)
		}

		capturedCh <- captured{
			method:      r.Method,
			path:        r.URL.Path,
			runnerID:    r.Header.Get("X-Runner-Id"),
			signature:   r.Header.Get("X-Runner-Signature"),
			responseID:  request.ResponseId,
			content:     request.StepsContent,
			contentType: r.Header.Get("Content-Type"),
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"step-1"}`))
	}))
	defer server.Close()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	dir := t.TempDir()
	configuration := map[string]string{
		"TemporalHost": "backend.example.com:7233",
		"ApiHost":      server.URL,
		"runnerId":     "runner-1",
		"runnerSecret": saveStepsTestSecret,
		"registryHost": "registry.example.com:5000",
	}
	raw, err := json.Marshal(configuration)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	steps := []model.Step{{Command: "build", Response: "ok", Success: true, Unixtime: 123}}
	SaveStepsToMono(SaveStepsCommand{ResponseId: "response-1", Steps: &steps})

	capturedRequest := <-capturedCh
	if capturedRequest.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", capturedRequest.method)
	}
	if capturedRequest.path != "/api/runner/saveSteps" {
		t.Fatalf("path = %q, want /api/runner/saveSteps", capturedRequest.path)
	}
	if capturedRequest.contentType != "application/json" {
		t.Fatalf("content-type = %q", capturedRequest.contentType)
	}
	if capturedRequest.runnerID != "runner-1" {
		t.Fatalf("X-Runner-Id = %q, want runner-1", capturedRequest.runnerID)
	}
	if capturedRequest.responseID != "response-1" {
		t.Fatalf("responseId = %q, want response-1", capturedRequest.responseID)
	}

	wantSignature := signSaveSteps(saveStepsTestSecret, capturedRequest.responseID, capturedRequest.content)
	if capturedRequest.signature != wantSignature {
		t.Fatalf("signature = %q, want %q", capturedRequest.signature, wantSignature)
	}
}
