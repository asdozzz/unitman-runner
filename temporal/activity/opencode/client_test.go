package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatCompletionsURL(t *testing.T) {
	cases := map[string]string{
		"https://example.test/v1":                   "https://example.test/v1/chat/completions",
		"https://example.test/v1/":                  "https://example.test/v1/chat/completions",
		"https://example.test/v1/chat/completions":  "https://example.test/v1/chat/completions",
		"https://example.test/v1/chat/completions/": "https://example.test/v1/chat/completions",
	}
	for in, want := range cases {
		if got := chatCompletionsURL(in); got != want {
			t.Fatalf("chatCompletionsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHTTPExecutorSendsAuthorizedChatRequest(t *testing.T) {
	const apiKey = "sk-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("unexpected auth header: %q", got)
		}

		var payload chatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("cannot decode request: %v", err)
		}
		if payload.Model != "gpt-5-codex" {
			t.Errorf("unexpected model: %s", payload.Model)
		}
		if len(payload.Messages) != 4 {
			t.Errorf("unexpected messages: %+v", payload.Messages)
		}
		if payload.Messages[3].Role != "user" || payload.Messages[3].Content != "новое" {
			t.Errorf("current prompt is not the last message: %+v", payload.Messages)
		}
		if len(payload.Tools) != 1 || payload.Tools[0].Function.Name != "ask_user" {
			t.Errorf("ask_user tool is missing: %+v", payload.Tools)
		}

		_, _ = io.WriteString(w, `{"session_id":"sess_1","choices":[{"message":{"content":"Готово"}}]}`)
	}))
	defer server.Close()

	executor := NewHTTPExecutor(server.Client())
	answer, err := executor.Execute(context.Background(), OpenCodeRequest{
		SessionID: "old",
		URL:       server.URL + "/v1",
		Model:     "gpt-5-codex",
		APIKey:    apiKey,
		Messages: []Replika{
			{Role: "user", Text: "старое"},
			{Role: "assistant", Text: "ответ"},
			{Role: "user", Text: "новое"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer.Text != "Готово" {
		t.Fatalf("unexpected text: %q", answer.Text)
	}
	if answer.SessionID == nil || *answer.SessionID != "sess_1" {
		t.Fatalf("unexpected session: %+v", answer.SessionID)
	}
}

func TestHTTPExecutorParsesAskUserToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"choices":[{
				"message":{
					"content":null,
					"tool_calls":[{
						"id":"call_1",
						"type":"function",
						"function":{
							"name":"ask_user",
							"arguments":"{\"questions\":[{\"id\":\"q1\",\"text\":\"Окружение?\",\"allowFreeText\":false,\"options\":[{\"id\":\"o1\",\"label\":\"stage\",\"description\":\"Тестовый стенд\"},{\"id\":\"o2\",\"label\":\"prod\"}]},{\"id\":\"q2\",\"text\":\"Комментарий?\"}]}"
						}
					}]
				}
			}]
		}`)
	}))
	defer server.Close()

	executor := NewHTTPExecutor(server.Client())
	answer, err := executor.Execute(context.Background(), OpenCodeRequest{
		URL:    server.URL,
		Model:  "model",
		APIKey: "key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(answer.Questions) != 2 {
		t.Fatalf("unexpected questions: %+v", answer.Questions)
	}
	if answer.Questions[0].ID != "q1" || len(answer.Questions[0].Options) != 2 {
		t.Fatalf("unexpected first question: %+v", answer.Questions[0])
	}
	if answer.Questions[0].Options[0].Description == nil || *answer.Questions[0].Options[0].Description != "Тестовый стенд" {
		t.Fatalf("unexpected description: %+v", answer.Questions[0].Options[0])
	}
	if !answer.Questions[1].AllowFreeText {
		t.Fatal("question without options must allow free text")
	}
}

func TestHTTPExecutorParsesStructuredContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]interface{}{
			"choices": []interface{}{
				map[string]interface{}{
					"message": map[string]interface{}{
						"content": "```json\n{\"questions\":[{\"id\":\"q1\",\"text\":\"Выбор?\",\"options\":[{\"id\":\"o1\",\"label\":\"А\"}]}]}\n```",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	executor := NewHTTPExecutor(server.Client())
	answer, err := executor.Execute(context.Background(), OpenCodeRequest{
		URL:    server.URL,
		Model:  "model",
		APIKey: "key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(answer.Questions) != 1 || answer.Questions[0].ID != "q1" {
		t.Fatalf("unexpected questions: %+v", answer.Questions)
	}
}

func TestHTTPExecutorParsesStructuredAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]interface{}{
			"choices": []interface{}{
				map[string]interface{}{
					"message": map[string]interface{}{
						"content": `{"answer":"Готово"}`,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	executor := NewHTTPExecutor(server.Client())
	answer, err := executor.Execute(context.Background(), OpenCodeRequest{
		URL:    server.URL,
		Model:  "model",
		APIKey: "key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer.Text != "Готово" || len(answer.Questions) != 0 {
		t.Fatalf("unexpected answer: %+v", answer)
	}
}

func TestHTTPExecutorUnexpectedResponse(t *testing.T) {
	for _, body := range []string{
		`not json`,
		`{"choices":[]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))

		executor := NewHTTPExecutor(server.Client())
		_, err := executor.Execute(context.Background(), OpenCodeRequest{
			URL:    server.URL,
			Model:  "model",
			APIKey: "key",
		})
		server.Close()

		if !errors.Is(err, ErrUnexpectedResponse) {
			t.Fatalf("expected ErrUnexpectedResponse for %q, got %v", body, err)
		}
	}
}

func TestHTTPExecutorDoesNotReturnApiKeyInError(t *testing.T) {
	const apiKey = "sk-very-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid key `+apiKey+`"}`)
	}))
	defer server.Close()

	executor := NewHTTPExecutor(server.Client())
	_, err := executor.Execute(context.Background(), OpenCodeRequest{
		URL:    server.URL,
		Model:  "model",
		APIKey: apiKey,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("api key leaked into error: %v", err)
	}
}

func TestBuildChatMessages(t *testing.T) {
	messages := buildChatMessages([]Replika{
		{Role: "USER", Text: "привет"},
		{Role: "tool", Text: "мусор"},
		{Role: "assistant", Text: "  "},
		{Role: "assistant", Text: "ответ"},
	})

	if len(messages) != 3 {
		t.Fatalf("unexpected messages: %+v", messages)
	}
	if messages[0].Role != "system" || messages[1].Role != "user" || messages[2].Role != "assistant" {
		t.Fatalf("unexpected roles: %+v", messages)
	}
}
