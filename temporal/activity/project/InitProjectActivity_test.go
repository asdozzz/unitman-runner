package project

import "testing"

func TestStripStorageURLCredentials(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantURL     string
		wantChanged bool
	}{
		{
			name:        "https with token",
			input:       "https://oauth2:glpat-secret@gitlab.example.com/group/repo.git",
			wantURL:     "https://gitlab.example.com/group/repo.git",
			wantChanged: true,
		},
		{
			name:        "https with username only",
			input:       "https://user@github.com/org/repo.git",
			wantURL:     "https://github.com/org/repo.git",
			wantChanged: true,
		},
		{
			name:        "already clean",
			input:       "https://gitlab.example.com/group/repo.git",
			wantURL:     "https://gitlab.example.com/group/repo.git",
			wantChanged: false,
		},
		{
			name:        "empty",
			input:       "",
			wantURL:     "",
			wantChanged: false,
		},
		{
			name:        "local path",
			input:       "/seed/git",
			wantURL:     "/seed/git",
			wantChanged: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gotURL, changed := stripStorageURLCredentials(testCase.input)
			if gotURL != testCase.wantURL {
				t.Fatalf("url = %q, want %q", gotURL, testCase.wantURL)
			}
			if changed != testCase.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, testCase.wantChanged)
			}
		})
	}
}
