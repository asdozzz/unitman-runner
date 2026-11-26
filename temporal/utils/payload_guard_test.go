package utils_test

// LT-4 / payload guard: representative Temporal activity inputs не должны
// содержать учётных данных. Guard объединяет unit prepare/build/update,
// workspace и project remove в одну таблицу (WS-3 покрывал только workspace),
// и переиспользует WS-4 redaction helpers для проверки очистки.

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"runner/temporal/activity/project"
	"runner/temporal/activity/unit"
	"runner/temporal/activity/workspace"
	"runner/temporal/utils"
)

const cleanStorageURL = "https://git.example.invalid/example/repo.git"

var (
	userInfoURLPattern      = regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`)
	credentialValuePatterns = []string{
		"oauth2:",
		"token@",
		"Bearer ",
		"ghp_",
		"github_pat_",
		"glpat-",
	}
	forbiddenKeyNames = map[string]bool{
		"token":         true,
		"password":      true,
		"secret":        true,
		"apikey":        true,
		"authorization": true,
		"credential":    true,
		"credentials":   true,
	}
)

// TestActivityInputsJSONHasNoCredentials — единый table-driven guard.
func TestActivityInputsJSONHasNoCredentials(t *testing.T) {
	cases := []struct {
		name          string
		payload       any
		expectStorURL bool
	}{
		{
			name:          "unit prepare",
			expectStorURL: true,
			payload: unit.NachatPodgotovkuUnita{
				ProjectId:   "project-1",
				ProjectName: "api",
				Id:          "unit-1",
				Name:        "unit",
				StorageUrl:  cleanStorageURL,
				RepoId:      "repo-1",
				Branch:      "main",
			},
		},
		{
			name:          "unit build",
			expectStorURL: true,
			payload: unit.NachatSborkuUnita{
				ProjectId:  "project-1",
				Id:         "unit-1",
				Name:       "unit",
				Branch:     "main",
				StorageUrl: cleanStorageURL,
				RepoId:     "repo-1",
				RunnerId:   "runner-1",
				TaskQueue:  "runner-1",
			},
		},
		{
			name:          "unit update",
			expectStorURL: true,
			payload: unit.NachatObnovlenieUnita{
				ProjectId:   "project-1",
				ProjectName: "api",
				Id:          "unit-1",
				Name:        "unit",
				StorageUrl:  cleanStorageURL,
				RepoId:      "repo-1",
			},
		},
		{
			name:          "workspace start",
			expectStorURL: true,
			payload: workspace.ZapustitWorkspaceCommand{
				WorkspaceId:   "ws-1",
				WorkspaceName: "workspace",
				WorkspaceSlug: "workspace",
				Branch:        "main",
				Projects: []workspace.Project{
					{
						ProjectId:   "project-1",
						ProjectName: "api",
						ProjectCode: "services/api",
						StorageUrl:  cleanStorageURL,
						RepoId:      "repo-1",
						MainBranch:  "main",
					},
				},
			},
		},
		{
			name: "project remove",
			payload: project.RemoveProjectCommand{
				ProjectId: "project-1",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := json.Marshal(c.payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			assertNoCredentialValues(t, string(raw))
			assertNoForbiddenKeys(t, string(raw))

			if c.expectStorURL && !strings.Contains(string(raw), `"StorageUrl":"`+cleanStorageURL+`"`) {
				t.Fatalf("clean StorageUrl must be preserved: %s", raw)
			}
		})
	}
}

// TestRedactionCleansCredentialBearingActivityPayload проверяет, что WS-4
// helpers действительно очищают креденшелы перед логированием/сериализацией.
func TestRedactionCleansCredentialBearingActivityPayload(t *testing.T) {
	payload := map[string]any{
		"StorageUrl": "https://oauth2:TESTTOKENEXAMPLEVALUE@git.example.invalid/example/repo.git",
		"Token":      "TESTTOKENEXAMPLEVALUE",
		"Password":   "TESTPASSWORDEXAMPLE",
		"secret":     "TESTSECRETEXAMPLE",
		"nested": []map[string]any{
			{"apiKey": "TESTAPIKEYEXAMPLE"},
		},
	}

	redacted := utils.RedactValue(payload)

	raw, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted: %v", err)
	}

	assertNoCredentialValues(t, string(raw))

	for _, synthetic := range []string{
		"TESTTOKENEXAMPLEVALUE",
		"TESTPASSWORDEXAMPLE",
		"TESTSECRETEXAMPLE",
		"TESTAPIKEYEXAMPLE",
	} {
		if strings.Contains(string(raw), synthetic) {
			t.Fatalf("synthetic credential %q leaked after redaction: %s", synthetic, raw)
		}
	}
}

func assertNoCredentialValues(t *testing.T, raw string) {
	t.Helper()

	for _, marker := range credentialValuePatterns {
		if strings.Contains(raw, marker) {
			t.Fatalf("payload contains credential marker %q: %s", marker, raw)
		}
	}

	if userInfoURLPattern.MatchString(raw) {
		t.Fatalf("payload contains user:password@ in URL: %s", raw)
	}
}

func assertNoForbiddenKeys(t *testing.T, raw string) {
	t.Helper()

	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	var violations []string
	collectForbiddenKeys(decoded, "$", &violations)

	if len(violations) > 0 {
		t.Fatalf("payload contains forbidden keys: %s", strings.Join(violations, ", "))
	}
}

func collectForbiddenKeys(node any, trail string, out *[]string) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if forbiddenKeyNames[normalizeGuardKey(key)] {
				*out = append(*out, trail+"."+key)
			}
			collectForbiddenKeys(child, trail+"."+key, out)
		}
	case []any:
		for _, child := range value {
			collectForbiddenKeys(child, trail+"[]", out)
		}
	}
}

func normalizeGuardKey(key string) string {
	normalized := strings.ToLower(key)
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, normalized)
}
