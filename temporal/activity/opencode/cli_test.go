package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"runner/temporal/activity/unit"
)

func TestCLIBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://example.test/v1":                   "https://example.test/v1",
		"https://example.test/v1/":                  "https://example.test/v1",
		"https://example.test/v1/chat/completions":  "https://example.test/v1",
		"https://example.test/v1/chat/completions/": "https://example.test/v1",
	}
	for in, want := range cases {
		if got := cliBaseURL(in); got != want {
			t.Fatalf("cliBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCLICommandUsesSessionOnlyWhenContinuing(t *testing.T) {
	req := OpenCodeRequest{
		SessionID: "sess_1",
		URL:       "https://example.test/v1",
		Model:     "gpt-5-codex",
		APIKey:    "sk-secret",
		WorkDir:   "/work/unit",
		Messages: []Replika{
			{Role: "user", Text: "старое"},
			{Role: "assistant", Text: "ответ"},
			{Role: "user", Text: "новое"},
		},
	}

	command := cliCommand(req, true)
	for _, want := range []string{
		"opencode run",
		"--format json",
		"--auto",
		"--model unitman/main",
		"--session sess_1",
		"новое",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("command %q does not contain %q", command, want)
		}
	}
	if strings.Contains(command, "История диалога") {
		t.Fatalf("continued session must not replay history: %q", command)
	}

	fresh := cliCommand(req, false)
	if strings.Contains(fresh, "--session") {
		t.Fatalf("fresh session must not pass --session: %q", fresh)
	}
	if !strings.Contains(fresh, "История диалога") {
		t.Fatalf("fresh session must replay history: %q", fresh)
	}
}

func TestCLIConfigJSONUsesCustomProvider(t *testing.T) {
	req := OpenCodeRequest{
		URL:    "https://example.test/v1/chat/completions",
		Model:  "gpt-5-codex",
		APIKey: "sk-secret",
	}

	raw, err := cliConfigJSON(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var config struct {
		Provider map[string]struct {
			NPM     string `json:"npm"`
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
			Models map[string]struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("cannot decode config: %v", err)
	}

	provider, ok := config.Provider["unitman"]
	if !ok {
		t.Fatalf("unitman provider is missing: %s", raw)
	}
	if provider.NPM != "@ai-sdk/openai-compatible" {
		t.Fatalf("unexpected provider package: %q", provider.NPM)
	}
	if provider.Options.BaseURL != "https://example.test/v1" {
		t.Fatalf("unexpected baseURL: %q", provider.Options.BaseURL)
	}
	if provider.Options.APIKey != "sk-secret" {
		t.Fatalf("unexpected apiKey: %q", provider.Options.APIKey)
	}
	if provider.Models["main"].ID != "gpt-5-codex" {
		t.Fatalf("unexpected model: %+v", provider.Models)
	}
}

func TestCLIExecutorRunsOpenCodeInUnitContainer(t *testing.T) {
	output := strings.Join([]string{
		`{"type":"step_start","sessionID":"ses_1","part":{"id":"p1","type":"step-start"}}`,
		`{"type":"text","sessionID":"ses_1","part":{"id":"p2","type":"text","text":"Готово"}}`,
		`{"type":"step_finish","sessionID":"ses_1","part":{"id":"p3","type":"step-finish"}}`,
	}, "\n")

	var (
		gotUnitPath string
		gotCommand  string
		gotOpts     unit.UnitExecOptions
	)
	executor := &CLIExecutor{ExecUnit: func(_ context.Context, unitPath, command string, opts unit.UnitExecOptions) (string, error) {
		gotUnitPath = unitPath
		gotCommand = command
		gotOpts = opts
		return output, nil
	}}

	answer, err := executor.Execute(context.Background(), OpenCodeRequest{
		URL:     "https://example.test/v1",
		Model:   "gpt-5-codex",
		APIKey:  "sk-secret",
		WorkDir: "/work/unit",
		Messages: []Replika{
			{Role: "user", Text: "привет"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer.Text != "Готово" {
		t.Fatalf("unexpected answer: %q", answer.Text)
	}
	if answer.SessionID == nil || *answer.SessionID != "ses_1" {
		t.Fatalf("unexpected session: %+v", answer.SessionID)
	}
	if gotUnitPath != "/work/unit" {
		t.Fatalf("unexpected unit path: %q", gotUnitPath)
	}
	if gotOpts.WorkDir != unitContainerAppDir {
		t.Fatalf("unexpected container workdir: %q", gotOpts.WorkDir)
	}
	// Ход по юниту не задаёт сервис явно: UnitExec подставит "unit".
	if gotOpts.Service != "" {
		t.Fatalf("unexpected compose service: %q", gotOpts.Service)
	}
	if !strings.Contains(gotCommand, "opencode run") {
		t.Fatalf("opencode run is missing: %q", gotCommand)
	}
	if !strings.Contains(strings.Join(gotOpts.Env, "\n"), "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("config env is missing: %#v", gotOpts.Env)
	}
	if !strings.Contains(strings.Join(gotOpts.Env, "\n"), "OPENCODE_DISABLE_AUTOUPDATE=1") {
		t.Fatalf("autoupdate env is missing: %#v", gotOpts.Env)
	}
}

func TestCLIExecutorRunsOpenCodeInWorkspaceContainer(t *testing.T) {
	output := `{"type":"text","sessionID":"ses_ws","part":{"id":"p1","type":"text","text":"Готово"}}`

	var gotOpts unit.UnitExecOptions
	executor := &CLIExecutor{ExecUnit: func(_ context.Context, _ string, _ string, opts unit.UnitExecOptions) (string, error) {
		gotOpts = opts
		return output, nil
	}}

	answer, err := executor.Execute(context.Background(), OpenCodeRequest{
		URL:       "https://example.test/v1",
		Model:     "gpt-5-codex",
		APIKey:    "sk-secret",
		WorkDir:   "/work/workspace",
		Workspace: true,
		Messages: []Replika{
			{Role: "user", Text: "привет"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer.Text != "Готово" {
		t.Fatalf("unexpected answer: %q", answer.Text)
	}
	// Workspace-ход исполняется в сервисе "workspace" и в каталоге /workspace.
	if gotOpts.Service != workspaceComposeService {
		t.Fatalf("unexpected compose service: %q", gotOpts.Service)
	}
	if gotOpts.WorkDir != workspaceContainerDir {
		t.Fatalf("unexpected container workdir: %q", gotOpts.WorkDir)
	}
}

func TestCLIExecutorParsesStructuredQuestion(t *testing.T) {
	questionJSON := `{"questions":[{"id":"q1","text":"Окружение?","allowFreeText":true,"options":[{"id":"o1","label":"stage"}]}]}`
	output := `{"type":"text","sessionID":"ses_1","part":{"id":"p1","type":"text","text":` + strconvQuote(questionJSON) + `}}`

	executor := &CLIExecutor{ExecUnit: func(context.Context, string, string, unit.UnitExecOptions) (string, error) {
		return output, nil
	}}

	answer, err := executor.Execute(context.Background(), OpenCodeRequest{
		URL:      "https://example.test/v1",
		Model:    "gpt-5-codex",
		APIKey:   "sk-secret",
		WorkDir:  "/work/unit",
		Messages: []Replika{{Role: "user", Text: "привет"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(answer.Questions) != 1 {
		t.Fatalf("unexpected questions: %+v", answer.Questions)
	}
	if answer.Questions[0].Text != "Окружение?" {
		t.Fatalf("unexpected question text: %+v", answer.Questions[0])
	}
}

func TestCLIExecutorReturnsEventError(t *testing.T) {
	output := `{"type":"error","sessionID":"ses_1","error":{"name":"UnknownError","data":{"message":"Model not found: unitman/main"}}}`

	executor := &CLIExecutor{ExecUnit: func(context.Context, string, string, unit.UnitExecOptions) (string, error) {
		return output, nil
	}}

	_, err := executor.Execute(context.Background(), OpenCodeRequest{
		URL:      "https://example.test/v1",
		Model:    "gpt-5-codex",
		APIKey:   "sk-secret",
		WorkDir:  "/work/unit",
		Messages: []Replika{{Role: "user", Text: "привет"}},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Model not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLIExecutorRecreatesLostSession(t *testing.T) {
	var commands []string
	executor := &CLIExecutor{ExecUnit: func(_ context.Context, _ string, command string, _ unit.UnitExecOptions) (string, error) {
		commands = append(commands, command)
		if len(commands) == 1 {
			return "", errors.New(`Session not found: sess_old`)
		}
		return `{"type":"text","sessionID":"ses_new","part":{"id":"p1","type":"text","text":"Готово"}}`, nil
	}}

	answer, err := executor.Execute(context.Background(), OpenCodeRequest{
		SessionID: "sess_old",
		URL:       "https://example.test/v1",
		Model:     "gpt-5-codex",
		APIKey:    "sk-secret",
		WorkDir:   "/work/unit",
		Messages: []Replika{
			{Role: "user", Text: "старое"},
			{Role: "assistant", Text: "ответ"},
			{Role: "user", Text: "новое"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer.Text != "Готово" || answer.SessionID == nil || *answer.SessionID != "ses_new" {
		t.Fatalf("unexpected answer: %+v", answer)
	}
	if len(commands) != 2 {
		t.Fatalf("expected two attempts, got %d", len(commands))
	}
	if !strings.Contains(commands[0], "--session sess_old") {
		t.Fatalf("first attempt must continue the old session: %q", commands[0])
	}
	if strings.Contains(commands[1], "--session") {
		t.Fatalf("second attempt must start a new session: %q", commands[1])
	}
	if !strings.Contains(commands[1], "История диалога") {
		t.Fatalf("second attempt must replay history: %q", commands[1])
	}
}

func TestParseCLIOutputIgnoresLogLines(t *testing.T) {
	output := strings.Join([]string{
		`timestamp=2026-11-15T10:28:34.304Z level=INFO message=stream`,
		`{"type":"text","sessionID":"ses_1","part":{"id":"p1","type":"text","text":"Ответ"}}`,
		`not json at all`,
	}, "\n")

	answer, err := parseCLIOutput(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer.Text != "Ответ" {
		t.Fatalf("unexpected text: %q", answer.Text)
	}
	if answer.SessionID == nil || *answer.SessionID != "ses_1" {
		t.Fatalf("unexpected session: %+v", answer.SessionID)
	}
}

func strconvQuote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
