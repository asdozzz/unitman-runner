package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"runner/config"
)

func okEnvelope(skills []Skill) string {
	data := map[string]any{
		"skills":       skills,
		"manifestHash": ManifestHash(skills),
		"totalBytes":   0,
		"truncated":    false,
	}
	raw, _ := json.Marshal(map[string]any{"status": "success", "data": data})
	return string(raw)
}

func testSkill(container, name, content string) Skill {
	return Skill{
		SkillID:       "id-" + container,
		Slug:          container,
		ContainerName: container,
		Name:          name,
		Description:   "description " + name,
		Content:       content,
		Version:       "1",
		Hash:          "hash-" + container,
		Scope:         "global",
		ProjectID:     "",
	}
}

func TestResolveSignsRawBodyAndParsesSuccess(t *testing.T) {
	skills := []Skill{testSkill("global-demo", "Demo", "# Demo\nbody\n")}
	var (
		gotRunnerID  string
		gotSignature string
		gotBody      []byte
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ResolvePath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		gotRunnerID = r.Header.Get(config.RunnerIDHeader)
		gotSignature = r.Header.Get(config.RunnerSignatureHeader)
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(okEnvelope(skills)))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, RunnerID: "runner-1", Secret: strings.Repeat("ab", 32)}
	result, err := client.Resolve(context.Background(), []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotRunnerID != "runner-1" {
		t.Fatalf("runner id header = %q", gotRunnerID)
	}
	if gotSignature != config.Sign(client.Secret, gotBody) {
		t.Fatalf("signature does not match raw body")
	}
	if len(result.Skills) != 1 || result.Skills[0].ContainerName != "global-demo" {
		t.Fatalf("unexpected skills: %+v", result.Skills)
	}

	var request resolveRequest
	if err := json.Unmarshal(gotBody, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.FormatVersion != FormatVersion || request.MaxTotalBytes != MaxTotalBytes {
		t.Fatalf("unexpected request: %+v", request)
	}
	if strings.Join(request.ProjectIDs, ",") != "p1,p2" {
		t.Fatalf("unexpected project ids: %+v", request.ProjectIDs)
	}
}

func TestResolve404DegradesToEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, RunnerID: "r", Secret: strings.Repeat("00", 32)}
	result, err := client.Resolve(context.Background(), []string{"p1"})
	if err != nil {
		t.Fatalf("404 must not be an error: %v", err)
	}
	if !result.NotDeployed {
		t.Fatal("expected NotDeployed for 404")
	}
	if result.ManifestHash != ManifestHash(nil) {
		t.Fatalf("unexpected empty hash: %q", result.ManifestHash)
	}
}

func TestResolveHTTPErrorIsReturned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"fail","message":"boom","code":"x"}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, RunnerID: "r", Secret: strings.Repeat("00", 32)}
	if _, err := client.Resolve(context.Background(), []string{"p1"}); err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestManifestHashContract(t *testing.T) {
	// sha256("") для пустого набора.
	sum := sha256.Sum256(nil)
	if got := ManifestHash(nil); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("empty hash = %q", got)
	}

	unordered := []Skill{
		{ContainerName: "global-b", Version: "2", Hash: "h2"},
		{ContainerName: "p1234-a", Version: "1", Hash: "h1"},
	}
	want := sha256.Sum256([]byte("global-b:2:h2\np1234-a:1:h1"))
	if got := ManifestHash(unordered); got != hex.EncodeToString(want[:]) {
		t.Fatalf("hash = %q, want %q", got, hex.EncodeToString(want[:]))
	}

	// Порядок на входе не влияет.
	reversed := []Skill{unordered[1], unordered[0]}
	if ManifestHash(reversed) != ManifestHash(unordered) {
		t.Fatal("manifest hash must be order-independent")
	}
}

func TestStageWritesLayoutAndManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "seed", "skills")
	skill := testSkill("p1234567-demo", "Demo Skill", "# Demo\n\nDo the thing.\n")
	result := ResolveResult{
		Skills:       []Skill{skill},
		ManifestHash: ManifestHash([]Skill{skill}),
		TotalBytes:   len(skill.Content),
	}

	outcome, err := Stage(root, result)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if outcome.Count != 1 || outcome.ManifestHash != ManifestHash([]Skill{skill}) {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}

	skillFile, err := os.ReadFile(filepath.Join(root, "p1234567-demo", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	for _, want := range []string{"---\n", `name: "Demo Skill"`, `description: "description Demo Skill"`, "Do the thing."} {
		if !strings.Contains(string(skillFile), want) {
			t.Fatalf("SKILL.md missing %q:\n%s", want, skillFile)
		}
	}

	manifestRaw, err := os.ReadFile(filepath.Join(root, ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest manifestFile
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ManifestHash != ManifestHash([]Skill{skill}) || manifest.SkillCount != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	indexRaw, err := os.ReadFile(filepath.Join(root, IndexFileName))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	index := string(indexRaw)
	for _, want := range []string{"# Agent Skills", "Demo Skill", "p1234567-demo/SKILL.md", "Scope: global"} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q:\n%s", want, index)
		}
	}

	info, err := os.Stat(filepath.Join(root, "p1234567-demo", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("SKILL.md mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestStageSkipsInvalidAndTraversalEntries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	result := ResolveResult{Skills: []Skill{
		testSkill("../escape", "Evil", "x"),
		testSkill("ok-good", "Good", "content"),
		testSkill("BadUpper", "Bad", "x"),
		testSkill("ok-good", "Duplicate", "x"),
	}}

	outcome, err := Stage(root, result)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if outcome.Count != 1 || outcome.Skipped != 3 {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if _, err := os.Stat(filepath.Join(root, "ok-good", "SKILL.md")); err != nil {
		t.Fatalf("valid skill was not staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); !os.IsNotExist(err) {
		t.Fatalf("path traversal escaped staging root: %v", err)
	}
}

func TestStageEnforcesPerSkillAndTotalCaps(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	tooBig := testSkill("global-big", "Big", strings.Repeat("a", maxSkillContentBytes+1))
	ok := testSkill("global-ok", "Ok", "content")

	outcome, err := Stage(root, ResolveResult{Skills: []Skill{tooBig, ok}})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if outcome.Count != 1 || outcome.Skipped != 1 {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}

func TestStageIsDeterministic(t *testing.T) {
	base := t.TempDir()
	skills := []Skill{
		testSkill("global-z", "Zeta", "z"),
		testSkill("global-a", "Alpha", "a"),
	}

	firstOutcome, err := Stage(filepath.Join(base, "one"), ResolveResult{Skills: skills})
	if err != nil {
		t.Fatal(err)
	}
	secondOutcome, err := Stage(filepath.Join(base, "two"), ResolveResult{Skills: skills})
	if err != nil {
		t.Fatal(err)
	}
	if firstOutcome.ManifestHash != secondOutcome.ManifestHash {
		t.Fatal("manifest hash must be deterministic")
	}

	read := func(root, name string) string {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if read(filepath.Join(base, "one"), IndexFileName) != read(filepath.Join(base, "two"), IndexFileName) {
		t.Fatal("skills-index.md must be deterministic")
	}
}

func TestStageFailureWritesErrorManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	StageFailure(root, "boom")

	raw, err := os.ReadFile(filepath.Join(root, ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest manifestFile
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.Error != "boom" {
		t.Fatalf("error marker missing: %+v", manifest)
	}
	if manifest.SkillCount != 0 || len(manifest.Skills) != 0 {
		t.Fatalf("failure manifest must be empty: %+v", manifest)
	}
	if manifest.ManifestHash != ManifestHash(nil) {
		t.Fatalf("unexpected manifest hash: %q", manifest.ManifestHash)
	}
}

// TestResolveAcceptsNumericAndStringVersion фиксирует контракт поля version:
// backend отдаёт его числом, а раннер обязан принять и число, и строку.
func TestResolveAcceptsNumericAndStringVersion(t *testing.T) {
	for _, versionJSON := range []string{`1`, `"1"`} {
		body := `{"status":"success","data":{"skills":[{`
		body += `"skillId":"id","slug":"demo","containerName":"global-demo",`
		body += `"name":"Demo","description":"d","content":"c",`
		body += `"version":` + versionJSON + `,"hash":"h",`
		body += `"scope":"global","projectId":null}],`
		body += `"manifestHash":"x","totalBytes":1,"truncated":false}}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))

		client := &Client{BaseURL: server.URL, RunnerID: "r", Secret: strings.Repeat("00", 32)}
		result, err := client.Resolve(context.Background(), []string{"p1"})
		server.Close()

		if err != nil {
			t.Fatalf("version %s: Resolve: %v", versionJSON, err)
		}
		if got := result.Skills[0].Version.String(); got != "1" {
			t.Fatalf("version %s: decoded version = %q", versionJSON, got)
		}
	}
}

func TestStageReplacesExistingTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	if _, err := Stage(root, ResolveResult{Skills: []Skill{testSkill("global-old", "Old", "old")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(root, ResolveResult{Skills: []Skill{testSkill("global-new", "New", "new")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "global-old")); !os.IsNotExist(err) {
		t.Fatalf("old skill dir must be removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "global-new", "SKILL.md")); err != nil {
		t.Fatalf("new skill dir missing: %v", err)
	}
}
