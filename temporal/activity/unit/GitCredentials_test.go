package unit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runner/temporal/credentials"
)

const testSyntheticToken = "TESTTOKENEXAMPLEVALUE"

func testGrant() credentials.Grant {
	return credentials.Grant{
		RepoID:    "repo-1",
		ProjectID: "project-1",
		RepoURL:   "https://git.example.com/org/repo.git",
		Username:  "oauth2",
		Token:     testSyntheticToken,
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
	}
}

func TestHostGitCredentialsKeepsTokenOutOfArgv(t *testing.T) {
	dir := t.TempDir()
	env, cleanup, err := HostGitCredentials(dir, testGrant())
	if err != nil {
		t.Fatalf("HostGitCredentials: %v", err)
	}
	defer cleanup()

	for _, entry := range env {
		if strings.Contains(entry, testSyntheticToken) {
			t.Fatalf("token leaked into environment: %q", entry)
		}
	}

	var askpassPath string
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_ASKPASS=") {
			askpassPath = strings.TrimPrefix(entry, "GIT_ASKPASS=")
		}
	}
	if askpassPath == "" {
		t.Fatal("GIT_ASKPASS is not configured")
	}

	script, err := os.ReadFile(askpassPath)
	if err != nil {
		t.Fatalf("read askpass: %v", err)
	}
	if strings.Contains(string(script), testSyntheticToken) {
		t.Fatalf("askpass script must not contain token: %s", script)
	}

	credPath := filepath.Join(filepath.Dir(askpassPath), "credentials")

	creds, err := os.ReadFile(credPath)
	if err != nil {
		t.Fatalf("read creds: %v", err)
	}
	if !strings.Contains(string(creds), testSyntheticToken) {
		t.Fatalf("credentials file should contain token for askpass")
	}
}

func TestCleanRepoURLRejectsUserinfo(t *testing.T) {
	for _, raw := range []string{
		"https://oauth2:TESTTOKENEXAMPLEVALUE@git.example.com/repo.git",
		"https://TESTTOKENEXAMPLEVALUE@git.example.com/repo.git",
	} {
		if _, err := CleanRepoURL(raw); err == nil {
			t.Fatalf("expected error for credential-bearing URL %q", raw)
		}
	}

	clean, err := CleanRepoURL("https://git.example.com/repo.git")
	if err != nil {
		t.Fatalf("clean URL rejected: %v", err)
	}
	if strings.Contains(clean, "@") {
		t.Fatalf("clean URL contains userinfo: %q", clean)
	}
}

func TestStripOriginURLCredentials(t *testing.T) {
	clean, changed := stripOriginURLCredentials("https://oauth2:TESTTOKENEXAMPLEVALUE@git.example.com/repo.git")
	if !changed {
		t.Fatal("expected changed=true for credential URL")
	}
	if strings.Contains(clean, "TESTTOKENEXAMPLEVALUE") || strings.Contains(clean, "@") {
		t.Fatalf("credentials not stripped: %q", clean)
	}
}

func TestActivityInputJSONContainsNoCredentialPatterns(t *testing.T) {
	cases := []any{
		NachatSborkuUnita{
			ProjectId:  "project-1",
			Id:         "unit-1",
			Name:       "unit",
			Branch:     "main",
			StorageUrl: "https://git.example.com/org/repo.git",
			RepoId:     "repo-1",
		},
		NachatPodgotovkuUnita{
			ProjectId:  "project-1",
			Id:         "unit-1",
			Name:       "unit",
			StorageUrl: "https://git.example.com/org/repo.git",
			RepoId:     "repo-1",
			Branch:     "main",
		},
		NachatObnovlenieUnita{
			ProjectId:  "project-1",
			Id:         "unit-1",
			Name:       "unit",
			StorageUrl: "https://git.example.com/org/repo.git",
			RepoId:     "repo-1",
		},
	}

	for _, input := range cases {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		for _, pattern := range []string{"oauth2:", "token@", "TESTTOKENEXAMPLEVALUE", "Bearer", "ghp_", "github_pat_", "user:pass@"} {
			if strings.Contains(string(raw), pattern) {
				t.Fatalf("activity input contains credential pattern %q: %s", pattern, raw)
			}
		}
		if !strings.Contains(string(raw), "https://git.example.com/org/repo.git") {
			t.Fatalf("StorageUrl must be clean: %s", raw)
		}
	}
}
