package opencode

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExecutor struct {
	answer OpenCodeAnswer
	err    error
	panic  interface{}
	calls  int
	block  bool
}

func (f *fakeExecutor) Execute(ctx context.Context, _ OpenCodeRequest) (OpenCodeAnswer, error) {
	f.calls++
	if f.panic != nil {
		panic(f.panic)
	}
	if f.block {
		<-ctx.Done()
		return OpenCodeAnswer{}, ctx.Err()
	}
	return f.answer, f.err
}

func validCommand() HodCommand {
	return HodCommand{
		Prompt: "Создай юнит",
		URL:    "https://opencode.internal/api",
		Model:  "gpt-5-codex",
		ApiKey: "sk-super-secret",
	}
}

func TestActivityAnswer(t *testing.T) {
	session := "sess_01"
	executor := &fakeExecutor{answer: OpenCodeAnswer{SessionID: &session, Text: "Готово"}}
	acts := &Activities{OpenCode: executor}

	result, err := acts.VipolnitHodOpenCodeActivity(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success || result.Kind != KindAnswer {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Answer == nil || *result.Answer != "Готово" {
		t.Fatalf("unexpected answer: %+v", result.Answer)
	}
	if result.Questions == nil || len(result.Questions) != 0 {
		t.Fatalf("questions must be an empty array: %#v", result.Questions)
	}
	if result.ExternalSessionID == nil || *result.ExternalSessionID != session {
		t.Fatalf("unexpected session: %+v", result.ExternalSessionID)
	}
}

func TestActivityKeepsKnownSession(t *testing.T) {
	executor := &fakeExecutor{answer: OpenCodeAnswer{Text: "Готово"}}
	acts := &Activities{OpenCode: executor}
	cmd := validCommand()
	cmd.SessionID = "sess_from_previous_turn"

	result, err := acts.VipolnitHodOpenCodeActivity(context.Background(), cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExternalSessionID == nil || *result.ExternalSessionID != cmd.SessionID {
		t.Fatalf("known session was lost: %+v", result.ExternalSessionID)
	}
}

func TestActivityQuestionReturnsImmediately(t *testing.T) {
	executor := &fakeExecutor{answer: OpenCodeAnswer{Questions: []Vopros{
		{ID: "q1", Text: "Окружение?", Options: []Variant{{ID: "o1", Label: "stage"}}},
	}}}
	acts := &Activities{OpenCode: executor}

	started := time.Now()
	result, err := acts.VipolnitHodOpenCodeActivity(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("question turn must not wait for an answer")
	}
	if !result.Success || result.Kind != KindQuestion || len(result.Questions) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Answer != nil || result.ErrorKey != nil {
		t.Fatalf("question must not carry answer/error: %+v", result)
	}
}

func TestActivityUnexpectedResponse(t *testing.T) {
	executor := &fakeExecutor{err: fmt.Errorf("%w: no choices", ErrUnexpectedResponse)}
	acts := &Activities{OpenCode: executor}

	result, err := acts.VipolnitHodOpenCodeActivity(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != KindError || result.ErrorKey == nil || *result.ErrorKey != ErrorKeyUnexpectedKind {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestActivityExecutionError(t *testing.T) {
	executor := &fakeExecutor{err: errors.New("connection refused: " + validCommand().ApiKey)}
	acts := &Activities{OpenCode: executor}

	result, err := acts.VipolnitHodOpenCodeActivity(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != KindError || result.ErrorKey == nil || *result.ErrorKey != ErrorKeyTurnFailed {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Questions == nil || len(result.Questions) != 0 {
		t.Fatalf("questions must be an empty array: %#v", result.Questions)
	}
}

func TestActivityPanicBecomesError(t *testing.T) {
	executor := &fakeExecutor{panic: "boom"}
	acts := &Activities{OpenCode: executor}

	result, err := acts.VipolnitHodOpenCodeActivity(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != KindError || result.ErrorKey == nil || *result.ErrorKey != ErrorKeyTurnFailed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestActivityCancellation(t *testing.T) {
	executor := &fakeExecutor{block: true}
	acts := &Activities{OpenCode: executor}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result HodResult
	var err error
	go func() {
		result, err = acts.VipolnitHodOpenCodeActivity(ctx, validCommand())
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("activity did not stop after context cancellation")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got result=%+v err=%v", result, err)
	}
}

func TestActivityWithoutExecutorReturnsError(t *testing.T) {
	acts := &Activities{}

	result, err := acts.VipolnitHodOpenCodeActivity(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != KindError || result.ErrorKey == nil || *result.ErrorKey != ErrorKeyTurnFailed {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestPrevratitRezultatBranches(t *testing.T) {
	session := "  sess  "
	text := "  ответ  "

	result := prevratitRezultat(OpenCodeAnswer{SessionID: &session, Text: text})
	if result.Kind != KindAnswer || result.Answer == nil || *result.Answer != text {
		t.Fatalf("unexpected answer result: %+v", result)
	}
	if result.ExternalSessionID == nil || *result.ExternalSessionID != "  sess  " {
		t.Fatalf("unexpected session: %+v", result.ExternalSessionID)
	}

	question := prevratitRezultat(OpenCodeAnswer{
		SessionID: &session,
		Text:      "и текст, и вопрос",
		Questions: []Vopros{{ID: "q1", Text: "Выбор?"}},
	})
	if question.Kind != KindQuestion || len(question.Questions) != 1 || question.Answer != nil {
		t.Fatalf("question must win over text: %+v", question)
	}

	empty := prevratitRezultat(OpenCodeAnswer{})
	if empty.Kind != KindError || empty.ErrorKey == nil || *empty.ErrorKey != ErrorKeyUnexpectedKind {
		t.Fatalf("unexpected empty result: %+v", empty)
	}

	blankSession := ""
	noSession := prevratitRezultat(OpenCodeAnswer{SessionID: &blankSession, Text: "ok"})
	if noSession.ExternalSessionID != nil {
		t.Fatalf("blank session must become null: %+v", noSession.ExternalSessionID)
	}
}

func TestNormalizeQuestions(t *testing.T) {
	description := "  "
	questions := normalizeQuestions([]Vopros{
		{Options: nil},
		{ID: "q1", Text: "Первый", Options: []Variant{{Label: ""}, {ID: "o1", Label: "ok", Description: &description}}},
		{ID: "q1", Text: "", Options: []Variant{{ID: "o1"}, {ID: "o1", Label: "dup"}}},
	})

	if questions[0].ID != "q1" {
		t.Fatalf("missing question id was not generated: %+v", questions[0])
	}
	if questions[0].Options == nil {
		t.Fatal("options must not be nil")
	}
	if questions[1].Options[0].ID == "" || questions[1].Options[0].Label == "" {
		t.Fatalf("option id/label was not filled: %+v", questions[1].Options[0])
	}
	if questions[2].ID == "q1" {
		t.Fatalf("duplicate question id was not renamed: %+v", questions[2])
	}
	if questions[2].Options[0].ID == questions[2].Options[1].ID {
		t.Fatalf("duplicate option id was not renamed: %+v", questions[2].Options)
	}
}

func TestSobratSoobsheniyaKeepsHistoryAndAppendsPrompt(t *testing.T) {
	cmd := validCommand()
	cmd.History = []Replika{{Role: "user", Text: "старое"}, {Role: "assistant", Text: "ответ"}}

	messages := sobratSoobsheniya(cmd)
	if len(messages) != 3 {
		t.Fatalf("unexpected messages: %+v", messages)
	}
	if messages[0].Text != "старое" || messages[1].Text != "ответ" || messages[2].Text != cmd.Prompt {
		t.Fatalf("unexpected order: %+v", messages)
	}
}

func TestHodCommandSerializationUsesArrays(t *testing.T) {
	data, err := json.Marshal(HodCommand{})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(data), `"Answers":[]`) || !strings.Contains(string(data), `"History":[]`) {
		t.Fatalf("Answers/History must be []: %s", data)
	}
}

func TestHodResultSerializationNeverHasNullQuestions(t *testing.T) {
	for _, result := range []HodResult{
		errorResult(ErrorKeyTurnFailed),
		{Success: true, Kind: KindAnswer, Questions: nil},
	} {
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		if !strings.Contains(string(data), `"Questions":[]`) {
			t.Fatalf("Questions must be []: %s", data)
		}
	}
}

func TestPodpis(t *testing.T) {
	token := "workflow-1"
	content := `[{"command":"x"}]`
	secret := "secret"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(token))
	mac.Write([]byte(content))
	expected := hex.EncodeToString(mac.Sum(nil))

	if got := podpis(token, content, secret); got != expected {
		t.Fatalf("unexpected signature: got %s want %s", got, expected)
	}
	if podpis(token, content, secret) != strings.ToLower(podpis(token, content, secret)) {
		t.Fatal("signature must be lowercase hex")
	}
}

func TestObrezatTekst(t *testing.T) {
	valid := strings.Repeat("a", stepResponseLimit+10)
	if got := obrezatTekst(valid); len(got) != stepResponseLimit {
		t.Fatalf("unexpected length: %d", len(got))
	}

	invalid := "ok\xff\xfe"
	if got := obrezatTekst(invalid); !strings.HasPrefix(got, "ok") {
		t.Fatalf("invalid UTF-8 was not fixed: %q", got)
	}
}

type capturedRequest struct {
	body    saveStepsRequest
	content string
	sign    string
}

func TestProgressSenderSendsFullSnapshotAndSignature(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []capturedRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/runner/saveSteps" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body saveStepsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("cannot decode body: %v", err)
		}
		mu.Lock()
		requests = append(requests, capturedRequest{body: body, content: body.StepsContent, sign: r.Header.Get("X-Runner-Signature")})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := newProgressSender(server.Client(), server.URL, "secret", "token-1")
	started := time.Now().Add(-5 * time.Second)
	p.addStep(context.Background(), "opencode model", "first", true, started)
	p.addStep(context.Background(), "opencode model", "second", false, started)
	p.publish(context.Background(), true, false)

	mu.Lock()
	defer mu.Unlock()
	if len(requests) == 0 {
		t.Fatal("no progress requests")
	}

	last := requests[len(requests)-1]
	if !last.body.Finished || last.body.Success {
		t.Fatalf("unexpected final flags: %+v", last.body)
	}
	if last.body.ResponseID != "token-1" {
		t.Fatalf("unexpected response id: %s", last.body.ResponseID)
	}
	if last.sign != podpis("token-1", last.content, "secret") {
		t.Fatal("signature does not match token+stepsContent")
	}

	var steps []Step
	if err := json.Unmarshal([]byte(last.content), &steps); err != nil {
		t.Fatalf("stepsContent is not a JSON array: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("snapshot must contain all steps, got %d", len(steps))
	}
	if steps[0].Command != "opencode model" || steps[1].Response != "second" {
		t.Fatalf("unexpected steps: %+v", steps)
	}
	for _, step := range steps {
		if step.UnixtimeStart == nil || *step.UnixtimeStart > step.Unixtime {
			t.Fatalf("invalid step times: %+v", step)
		}
	}
}

func TestProgressSenderRetriesServerErrors(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		current := calls
		mu.Unlock()
		if current == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := newProgressSender(server.Client(), server.URL, "", "token")
	p.publish(context.Background(), true, true)

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected one retry, got %d calls", calls)
	}
}

func TestProgressSenderDoesNotRetryBadRequest(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"status":"fail","data":{"message":"runner.invalid_signature"}}`)
	}))
	defer server.Close()

	p := newProgressSender(server.Client(), server.URL, "secret", "token")
	p.publish(context.Background(), true, true)

	if calls != 1 {
		t.Fatalf("4xx must not be retried, got %d calls", calls)
	}
}

func TestProgressSenderEmptySnapshotIsArray(t *testing.T) {
	var content string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body saveStepsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("cannot decode body: %v", err)
		}
		content = body.StepsContent
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := newProgressSender(server.Client(), server.URL, "", "token")
	p.publish(context.Background(), true, false)

	if content != "[]" {
		t.Fatalf("empty snapshot must be [], got %q", content)
	}
}

func TestProgressSenderSendsFinalOnlyOnce(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := newProgressSender(server.Client(), server.URL, "", "token")
	p.publish(context.Background(), true, true)
	p.publish(context.Background(), true, true)

	if calls != 1 {
		t.Fatalf("final snapshot must be sent exactly once, calls=%d", calls)
	}
}

func TestProgressSenderCanceledContextStillSendsFinalSnapshot(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := newProgressSender(server.Client(), server.URL, "", "token")
	p.publish(ctx, true, false)

	if calls != 1 {
		t.Fatalf("final snapshot was not sent with detached context, calls=%d", calls)
	}
}

func TestProgressSenderSkipsWhenTokenIsEmpty(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := newProgressSender(server.Client(), server.URL, "", "")
	p.publish(context.Background(), true, false)

	if calls != 0 {
		t.Fatalf("progress without token must be skipped, calls=%d", calls)
	}
}
