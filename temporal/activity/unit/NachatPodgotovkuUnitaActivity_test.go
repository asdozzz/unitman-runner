package unit

import (
	"strings"
	"testing"
)

// TestResolveRegistryCanonicalFormat закрепляет канонический вид
// scheme://host[:port] без завершающего слэша и authority host[:port],
// который попадает в UNITMAN_CACHE_REPO.
func TestResolveRegistryCanonicalFormat(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantHost      string
		wantAuthority string
	}{
		{
			name:          "bare host and port",
			input:         "registry.example:5000",
			wantHost:      "http://registry.example:5000",
			wantAuthority: "registry.example:5000",
		},
		{
			name:          "https with trailing slash",
			input:         "https://registry.example:5000/",
			wantHost:      "https://registry.example:5000",
			wantAuthority: "registry.example:5000",
		},
		{
			name:          "scheme and host without port",
			input:         "http://registry.example",
			wantHost:      "http://registry.example",
			wantAuthority: "registry.example",
		},
		{
			name:          "surrounding whitespace",
			input:         "  http://registry.example:5000/  ",
			wantHost:      "http://registry.example:5000",
			wantAuthority: "registry.example:5000",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gotHost, gotAuthority, err := ResolveRegistry(testCase.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotHost != testCase.wantHost {
				t.Fatalf("registry host = %q, want %q", gotHost, testCase.wantHost)
			}
			if gotAuthority != testCase.wantAuthority {
				t.Fatalf("cache authority = %q, want %q", gotAuthority, testCase.wantAuthority)
			}
		})
	}
}

// TestResolveRegistryRequiresHost проверяет, что без адреса реестра
// возвращается понятная ошибка и нет fallback на прежние имена.
func TestResolveRegistryRequiresHost(t *testing.T) {
	for _, input := range []string{"", "   ", "/"} {
		_, _, err := ResolveRegistry(input)
		if err == nil {
			t.Fatalf("expected error for %q", input)
		}
		if !strings.Contains(err.Error(), "registryHost") {
			t.Fatalf("error %q should mention registryHost", err.Error())
		}
	}
}
