package credentials

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"runner/config"
)

func TestFetchSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Path {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get(config.RunnerSignatureHeader) == "" {
			t.Fatal("missing signature")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"repoId":"repo-1",
			"projectId":"project-1",
			"repoUrl":"https://git.example.com/org/repo.git",
			"username":"x-access-token",
			"token":"TESTTOKENEXAMPLEVALUE",
			"expiresAt":` + futureUnix() + `
		}`))
	}))
	defer server.Close()

	grant, err := NewClient(server.URL, "runner-1", "secret", nil).
		Fetch(context.Background(), "repo-1", "project-1", "unit-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if grant.Token != "TESTTOKENEXAMPLEVALUE" {
		t.Fatalf("unexpected token: %q", grant.Token)
	}
	if strings.Contains(grant.RepoURL, "@") {
		t.Fatalf("repoUrl must not contain userinfo: %q", grant.RepoURL)
	}
}

func TestFetchAuthAndAuthzErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()

			_, err := NewClient(server.URL, "runner-1", "secret", nil).
				Fetch(context.Background(), "repo-1", "project-1", "")
			if err == nil {
				t.Fatalf("expected error for status %d", status)
			}
			if strings.Contains(err.Error(), "TESTTOKENEXAMPLEVALUE") {
				t.Fatalf("error leaked token: %v", err)
			}
		})
	}
}

func TestFetchRejectsExpiredAndUserinfo(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "expired",
			body: `{"repoId":"repo-1","projectId":"project-1","repoUrl":"https://git.example.com/repo.git","username":"oauth2","token":"TESTTOKENEXAMPLEVALUE","expiresAt":1}`,
		},
		{
			name: "userinfo",
			body: `{"repoId":"repo-1","projectId":"project-1","repoUrl":"https://oauth2:TESTTOKENEXAMPLEVALUE@git.example.com/repo.git","username":"oauth2","token":"TESTTOKENEXAMPLEVALUE","expiresAt":` + futureUnix() + `}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()

			_, err := NewClient(server.URL, "runner-1", "secret", nil).
				Fetch(context.Background(), "repo-1", "project-1", "")
			if err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
}

func futureUnix() string {
	return strconv.FormatInt(time.Now().Add(5*time.Minute).Unix(), 10)
}
