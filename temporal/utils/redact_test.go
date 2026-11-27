package utils

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// Синтетические литералы для тестов (не являются реальными креденшелами).
const (
	testOAuthTokenURL = "https://oauth2:TESTTOKENEXAMPLEVALUE@github.com/example/repo.git"
	testUserPassURL   = "https://TESTUSEREXAMPLE:TESTPASSWORDEXAMPLE@git.example.com/example/repo.git"
	testBareOAuth     = "oauth2:TESTTOKENEXAMPLEVALUE"
	testAuthorization = "Authorization: Bearer TESTJWTEXAMPLEVALUE"
	testBareBearer    = "Bearer TESTJWTEXAMPLEVALUE"
)

// representativeCommand повторяет форму команд активностей, содержащих
// StorageUrl с встроенным токеном.
type representativeCommand struct {
	ProjectId   string
	ProjectName string
	Id          string
	Name        string
	StorageUrl  string
	Variables   []map[string]any
}

type nestedCredentials struct {
	ApiKey string
	Note   string
}

type commandWithCredentialFields struct {
	ProjectId     string
	StorageUrl    string
	Token         string
	ApiKey        string
	Password      string
	Secret        string
	Authorization string
	Nested        nestedCredentials
	Items         []map[string]any
}

func TestRedactString(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "url userinfo with oauth2 token",
			input: "git clone " + testOAuthTokenURL,
			want:  "git clone https://" + RedactedValue + "@github.com/example/repo.git",
		},
		{
			name:  "url userinfo with user and password",
			input: testUserPassURL,
			want:  "https://" + RedactedValue + "@git.example.com/example/repo.git",
		},
		{
			name:  "bare oauth2 credential",
			input: "credential is " + testBareOAuth,
			want:  "credential is oauth2:" + RedactedValue,
		},
		{
			name:  "authorization bearer header",
			input: testAuthorization,
			want:  "Authorization: " + RedactedValue,
		},
		{
			name:  "bare bearer token",
			input: "header " + testBareBearer,
			want:  "header Bearer " + RedactedValue,
		},
		{
			name:  "sensitive query parameters",
			input: "https://git.example.com/example/repo.git?token=TESTTOKENEXAMPLEVALUE&access_token=TESTACCESSTOKENEXAMPLE&private_token=TESTPRIVATETOKENEXAMPLE&api_key=TESTAPIKEYEXAMPLE&client_secret=TESTCLIENTSECRETEXAMPLE&branch=main",
			want:  "https://git.example.com/example/repo.git?token=" + RedactedValue + "&access_token=" + RedactedValue + "&private_token=" + RedactedValue + "&api_key=" + RedactedValue + "&client_secret=" + RedactedValue + "&branch=main",
		},
		{
			name:  "case insensitive query parameters",
			input: "https://git.example.com/repo.git?TOKEN=TESTTOKENEXAMPLEVALUE&Api_Key=TESTAPIKEYEXAMPLE",
			want:  "https://git.example.com/repo.git?TOKEN=" + RedactedValue + "&Api_Key=" + RedactedValue,
		},
		{
			name:  "no credentials unchanged",
			input: "https://git.example.com/example/repo.git",
			want:  "https://git.example.com/example/repo.git",
		},
		{
			name:  "plain text unchanged",
			input: "plain text",
			want:  "plain text",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactString(c.input); got != c.want {
				t.Fatalf("RedactString(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestRedactURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "oauth2 userinfo",
			input: testOAuthTokenURL,
			want:  "https://" + RedactedValue + "@github.com/example/repo.git",
		},
		{
			name:  "sensitive query parameter",
			input: "https://git.example.com/repo.git?token=TESTTOKENEXAMPLEVALUE&x=1",
			want:  "https://git.example.com/repo.git?token=" + RedactedValue + "&x=1",
		},
		{
			name:  "scp-like address fail closed",
			input: "git@github.com:example/repo.git",
			want:  RedactedValue,
		},
		{
			name:  "unparseable url fail closed",
			input: "https://oauth2:TESTTOKENEXAMPLEVALUE@github.com/%zz",
			want:  RedactedValue,
		},
		{
			name:  "no credentials unchanged",
			input: "https://github.com/example/repo.git",
			want:  "https://github.com/example/repo.git",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactURL(c.input); got != c.want {
				t.Fatalf("RedactURL(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestRedactValueRecursesNestedStructures(t *testing.T) {
	command := commandWithCredentialFields{
		ProjectId:     "project-1",
		StorageUrl:    testOAuthTokenURL,
		Token:         "TESTTOKENEXAMPLEVALUE",
		ApiKey:        "TESTAPIKEYEXAMPLE",
		Password:      "TESTPASSWORDEXAMPLE",
		Secret:        "TESTSECRETEXAMPLE",
		Authorization: "Bearer TESTJWTEXAMPLEVALUE",
		Nested: nestedCredentials{
			ApiKey: "TESTNESTEDAPIKEYEXAMPLE",
			Note:   "https://oauth2:TESTNESTEDTOKENEXAMPLE@github.com/example/nested.git",
		},
		Items: []map[string]any{
			{
				"access_token": "TESTACCESSTOKENEXAMPLE",
				"url":          "https://oauth2:TESTNESTEDMAPTOKEN@github.com/example/map.git",
			},
		},
	}

	redacted := RedactValue(command).(map[string]any)

	if redacted["StorageUrl"] != "https://"+RedactedValue+"@github.com/example/repo.git" {
		t.Fatalf("StorageUrl not safely redacted: %v", redacted["StorageUrl"])
	}
	for _, field := range []string{"Token", "ApiKey", "Password", "Secret", "Authorization"} {
		if redacted[field] != RedactedValue {
			t.Fatalf("%s not fully redacted: %v", field, redacted[field])
		}
	}

	nested := redacted["Nested"].(map[string]any)
	if nested["ApiKey"] != RedactedValue {
		t.Fatalf("nested ApiKey not redacted: %v", nested["ApiKey"])
	}
	if strings.Contains(nested["Note"].(string), "TESTNESTEDTOKENEXAMPLE") {
		t.Fatalf("nested URL token leaked: %v", nested["Note"])
	}

	items := redacted["Items"].([]any)
	item := items[0].(map[string]any)
	if item["access_token"] != RedactedValue {
		t.Fatalf("nested map token not redacted: %v", item["access_token"])
	}
	if strings.Contains(item["url"].(string), "TESTNESTEDMAPTOKEN") {
		t.Fatalf("nested map URL token leaked: %v", item["url"])
	}

	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted value: %v", err)
	}
	assertNoSyntheticSecrets(t, string(encoded))
}

func TestRedactFieldsProtectsFutureStorageURL(t *testing.T) {
	fields := map[string]any{
		"StorageUrl": testOAuthTokenURL,
		"Token":      "TESTTOKENEXAMPLEVALUE",
		"Safe":       "keep me",
		"nested": map[string]any{
			"StorageUrl": testUserPassURL,
			"api_key":    "TESTAPIKEYEXAMPLE",
		},
	}

	redacted := RedactFields(fields)

	if redacted["StorageUrl"] != "https://"+RedactedValue+"@github.com/example/repo.git" {
		t.Fatalf("StorageUrl not redacted: %v", redacted["StorageUrl"])
	}
	if redacted["Token"] != RedactedValue {
		t.Fatalf("Token not redacted: %v", redacted["Token"])
	}
	if redacted["Safe"] != "keep me" {
		t.Fatalf("safe field changed: %v", redacted["Safe"])
	}
	nested := redacted["nested"].(map[string]any)
	if nested["api_key"] != RedactedValue {
		t.Fatalf("nested api_key not redacted: %v", nested["api_key"])
	}
	if strings.Contains(nested["StorageUrl"].(string), "TESTPASSWORDEXAMPLE") {
		t.Fatalf("nested StorageUrl leaked: %v", nested["StorageUrl"])
	}
}

func TestActivityCommandLoggingDoesNotLeakCredentials(t *testing.T) {
	command := representativeCommand{
		ProjectId:   "project-1",
		ProjectName: "example",
		Id:          "unit-1",
		Name:        "unit",
		StorageUrl:  testOAuthTokenURL,
		Variables: []map[string]any{
			{"id": "ACCESS_TOKEN", "value": "TESTACCESSTOKENEXAMPLE"},
		},
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	LogCommand(logger, "NachatObnovlenieUnita", command)

	output := buf.String()
	if strings.TrimSpace(output) == "" {
		t.Fatal("expected log output, got empty buffer")
	}

	for _, forbidden := range []string{
		"oauth2:",
		"token@",
		"TESTTOKENEXAMPLEVALUE",
		"TESTACCESSTOKENEXAMPLE",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("credential material %q leaked into log output: %s", forbidden, output)
		}
	}

	if !strings.Contains(output, RedactedValue) {
		t.Fatalf("expected redaction marker in log output: %s", output)
	}
}

func TestRedactsHiddenUnitVariableCipher(t *testing.T) {
	type variable struct {
		Id          string
		Value       string
		ValueCipher string
		Hidden      bool
	}

	redacted := RedactValue(variable{
		Id:          "PV_FOO",
		ValueCipher: "TESTCIPHEREXAMPLEVALUE",
		Hidden:      true,
	})

	fields, ok := redacted.(map[string]any)
	if !ok {
		t.Fatalf("RedactValue() = %T, want map", redacted)
	}
	if fields["ValueCipher"] != RedactedValue {
		t.Fatalf("ValueCipher = %v, want %q", fields["ValueCipher"], RedactedValue)
	}
}

func assertNoSyntheticSecrets(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{
		"TESTTOKENEXAMPLEVALUE",
		"TESTPASSWORDEXAMPLE",
		"TESTJWTEXAMPLEVALUE",
		"TESTACCESSTOKENEXAMPLE",
		"TESTAPIKEYEXAMPLE",
		"TESTCLIENTSECRETEXAMPLE",
		"TESTNESTEDAPIKEYEXAMPLE",
		"TESTNESTEDTOKENEXAMPLE",
		"TESTNESTEDMAPTOKEN",
		"oauth2:",
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("synthetic secret %q leaked: %s", secret, value)
		}
	}
}
