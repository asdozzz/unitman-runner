package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// containerNameRe — единственный допустимый вид containerName: безопасный
// сегмент пути, globals — global-<slug>, проекты — p<first8>-<slug>.
var containerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

const (
	// IndexFileName — имя сводного индекса внутри staging-каталога.
	IndexFileName = "skills-index.md"
	// ManifestFileName — имя манифеста внутри staging-каталога.
	ManifestFileName = "manifest.json"
	// SkillFileName — имя файла навыка в его каталоге.
	SkillFileName = "SKILL.md"

	maxSkillNameBytes        = 256
	maxSkillDescriptionBytes = 4096
	maxSkillVersionBytes     = 128
	maxSkillHashBytes        = 256
)

// Outcome — краткий итог staging-а для шага прогресса.
type Outcome struct {
	Count        int
	ManifestHash string
	TotalBytes   int
	Truncated    bool
	NotDeployed  bool
	Skipped      int
}

// manifestFile — то, что читает in-container unit-skills-sync.sh.
type manifestFile struct {
	ManifestHash string          `json:"manifestHash"`
	GeneratedAt  string          `json:"generatedAt"`
	TotalBytes   int             `json:"totalBytes"`
	Truncated    bool            `json:"truncated"`
	SkillCount   int             `json:"skillCount"`
	Error        string          `json:"error,omitempty"`
	Skills       []manifestSkill `json:"skills"`
}

type manifestSkill struct {
	ContainerName string `json:"containerName"`
	Slug          string `json:"slug,omitempty"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Hash          string `json:"hash"`
	Scope         string `json:"scope,omitempty"`
	ProjectID     string `json:"projectId,omitempty"`
}

// Stage безопасно раскладывает навыки в staging-каталог root через временный
// каталог и атомарную замену.
//
// Невалидные навыки пропускаются с предупреждением в лог. При ошибке записи
// Stage пытается оставить manifest.json с маркером ошибки и пустым набором,
// чтобы контейнерный sync не сломался.
func Stage(root string, result ResolveResult) (Outcome, error) {
	accepted, skipped, totalBytes, truncated := filterSkills(result.Skills)

	outcome := Outcome{
		Count:       len(accepted),
		TotalBytes:  totalBytes,
		Truncated:   truncated || result.Truncated,
		NotDeployed: result.NotDeployed,
		Skipped:     skipped,
	}

	computedHash := ManifestHash(accepted)
	outcome.ManifestHash = computedHash

	if result.ManifestHash != "" && result.ManifestHash != computedHash {
		log.Printf("skills: manifestHash mismatch: backend=%q computed=%q (staged=%d skipped=%d)",
			result.ManifestHash, computedHash, len(accepted), skipped)
	}

	manifest := buildManifest(accepted, outcome)

	if err := writeAtomic(root, accepted, manifest); err != nil {
		StageFailure(root, err.Error())
		return outcome, err
	}

	return outcome, nil
}

// StageFailure пишет manifest.json с маркером ошибки и пустым набором навыков.
// Ошибки записи игнорируются: это best-effort деградация.
func StageFailure(root, message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "skills staging failed"
	}
	message = truncateForLog(message)

	manifest := manifestFile{
		ManifestHash: ManifestHash(nil),
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		SkillCount:   0,
		Error:        message,
		Skills:       []manifestSkill{},
	}

	if err := writeAtomic(root, nil, manifest); err != nil {
		log.Printf("skills: cannot write failure manifest to %s: %v", root, err)
	}
}

// FetchAndStage — удобная точка входа для активностей: читает config.json,
// выполняет подписанный resolve и раскладывает результат. Любая ошибка уже
// оставляет (если возможно) failure-manifest и возвращается вызывающему,
// который обязан трактовать её как некритичную.
func FetchAndStage(ctx context.Context, root string, projectIDs []string) (Outcome, error) {
	client, err := NewClientFromConfig()
	if err != nil {
		StageFailure(root, err.Error())
		return Outcome{}, err
	}

	result, err := client.Resolve(ctx, projectIDs)
	if err != nil {
		StageFailure(root, err.Error())
		return Outcome{}, err
	}

	outcome, err := Stage(root, result)
	if err != nil {
		return outcome, err
	}

	return outcome, nil
}

func filterSkills(skills []Skill) (accepted []Skill, skipped int, totalBytes int, truncated bool) {
	accepted = make([]Skill, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))

	for _, skill := range skills {
		reason := validateSkill(skill)
		if reason != "" {
			log.Printf("skills: skip %q: %s", skill.ContainerName, reason)
			skipped++
			continue
		}

		if _, ok := seen[skill.ContainerName]; ok {
			log.Printf("skills: skip %q: duplicate containerName", skill.ContainerName)
			skipped++
			continue
		}

		size := len([]byte(skill.Content))
		if totalBytes+size > maxTotalContentBytes {
			log.Printf("skills: skip %q: total size limit %d exceeded", skill.ContainerName, maxTotalContentBytes)
			skipped++
			truncated = true
			continue
		}

		seen[skill.ContainerName] = struct{}{}
		totalBytes += size
		accepted = append(accepted, skill)
	}

	sort.Slice(accepted, func(i, j int) bool {
		return accepted[i].ContainerName < accepted[j].ContainerName
	})

	return accepted, skipped, totalBytes, truncated
}

// validateSkill проверяет имя каталога и размеры. Возвращает пустую строку,
// если навык валиден.
func validateSkill(skill Skill) string {
	name := skill.ContainerName
	if name == "" {
		return "empty containerName"
	}
	if !containerNameRe.MatchString(name) {
		return "invalid containerName"
	}
	// Дополнительная защита от обхода пути даже при будущем ослаблении regexp.
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "path traversal in containerName"
	}
	if strings.TrimSpace(skill.Name) == "" {
		return "empty name"
	}
	if len(skill.Name) > maxSkillNameBytes {
		return "name too long"
	}
	if len(skill.Description) > maxSkillDescriptionBytes {
		return "description too long"
	}
	if len(string(skill.Version)) > maxSkillVersionBytes {
		return "version too long"
	}
	if len(skill.Hash) > maxSkillHashBytes {
		return "hash too long"
	}
	if strings.ContainsRune(skill.Content, '\x00') {
		return "content contains NUL"
	}
	if len([]byte(skill.Content)) > maxSkillContentBytes {
		return fmt.Sprintf("content exceeds %d bytes", maxSkillContentBytes)
	}

	return ""
}

func buildManifest(accepted []Skill, outcome Outcome) manifestFile {
	entries := make([]manifestSkill, 0, len(accepted))
	for _, skill := range accepted {
		entries = append(entries, manifestSkill{
			ContainerName: skill.ContainerName,
			Slug:          skill.Slug,
			Name:          skill.Name,
			Version:       string(skill.Version),
			Hash:          skill.Hash,
			Scope:         skill.Scope,
			ProjectID:     skill.ProjectID,
		})
	}

	return manifestFile{
		ManifestHash: outcome.ManifestHash,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		TotalBytes:   outcome.TotalBytes,
		Truncated:    outcome.Truncated,
		SkillCount:   len(accepted),
		Skills:       entries,
	}
}

// writeAtomic пишет staging во временный каталог рядом с root и атомарно
// подменяет целевой каталог.
func writeAtomic(root string, accepted []Skill, manifest manifestFile) error {
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create staging parent %s: %w", parent, err)
	}

	tmp, err := os.MkdirTemp(parent, ".skills-tmp-")
	if err != nil {
		return fmt.Errorf("create staging temp dir: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmp)
		}
	}()

	for _, skill := range accepted {
		dir := filepath.Join(tmp, skill.ContainerName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create skill dir %s: %w", skill.ContainerName, err)
		}
		content := renderSkillFile(skill)
		if err := os.WriteFile(filepath.Join(dir, SkillFileName), content, 0o644); err != nil {
			return fmt.Errorf("write skill %s: %w", skill.ContainerName, err)
		}
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(tmp, ManifestFileName), manifestBytes, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if err := os.WriteFile(filepath.Join(tmp, IndexFileName), renderIndex(accepted), 0o644); err != nil {
		return fmt.Errorf("write skills index: %w", err)
	}

	if err := replaceDir(root, tmp); err != nil {
		return err
	}
	committed = true

	return nil
}

// replaceDir подменяет root содержимым tmp, сохраняя старую копию до успешной
// подмены.
func replaceDir(root, tmp string) error {
	backup := root + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	hadExisting := false
	if _, err := os.Lstat(root); err == nil {
		if err := os.Rename(root, backup); err != nil {
			return fmt.Errorf("move old skills dir aside: %w", err)
		}
		hadExisting = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat skills dir: %w", err)
	}

	if err := os.Rename(tmp, root); err != nil {
		if hadExisting {
			_ = os.Rename(backup, root)
		}
		return fmt.Errorf("swap skills dir: %w", err)
	}

	if hadExisting {
		_ = os.RemoveAll(backup)
	}

	return nil
}

func renderSkillFile(skill Skill) []byte {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("name: ")
	builder.WriteString(yamlDoubleQuote(skill.Name))
	builder.WriteString("\n")
	builder.WriteString("description: ")
	builder.WriteString(yamlDoubleQuote(sanitizeSingleLine(skill.Description)))
	builder.WriteString("\n")
	builder.WriteString("---\n\n")
	builder.WriteString(strings.TrimRight(skill.Content, "\r\n"))
	builder.WriteString("\n")

	return []byte(builder.String())
}

// renderIndex собирает детерминированный индекс навыков. Порядок — по
// containerName (accepted уже отсортирован).
func renderIndex(accepted []Skill) []byte {
	var builder strings.Builder
	builder.WriteString("# Agent Skills\n\n")

	if len(accepted) == 0 {
		builder.WriteString("No agent skills are available for this environment.\n")
		return []byte(builder.String())
	}

	builder.WriteString("These are agent skills resolved by Unitman for this environment. ")
	builder.WriteString("Each skill lives in its own directory and contains a SKILL.md file.\n\n")
	builder.WriteString("When a task matches a skill below, read the SKILL.md file in that skill's ")
	builder.WriteString("directory and follow its instructions before continuing.\n\n")
	builder.WriteString("## Available skills\n\n")

	for _, skill := range accepted {
		scope := strings.TrimSpace(skill.Scope)
		if scope == "" {
			scope = "unknown"
		}
		project := strings.TrimSpace(skill.ProjectID)
		if project == "" {
			project = "global"
		}

		builder.WriteString("### ")
		builder.WriteString(sanitizeSingleLine(skill.Name))
		builder.WriteString("\n")
		builder.WriteString("- Scope: ")
		builder.WriteString(scope)
		builder.WriteString("\n")
		builder.WriteString("- Project: ")
		builder.WriteString(project)
		builder.WriteString("\n")
		builder.WriteString("- Directory: `")
		builder.WriteString(skill.ContainerName)
		builder.WriteString("`\n")
		builder.WriteString("- Instructions: read `")
		builder.WriteString(skill.ContainerName)
		builder.WriteString("/")
		builder.WriteString(SkillFileName)
		builder.WriteString("` when relevant\n")
		builder.WriteString("- Description: ")
		builder.WriteString(sanitizeSingleLine(skill.Description))
		builder.WriteString("\n\n")
	}

	return []byte(builder.String())
}

func sanitizeSingleLine(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

// yamlDoubleQuote экранирует значение для YAML double-quoted scalar.
func yamlDoubleQuote(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, runeValue := range value {
		switch runeValue {
		case '\\':
			builder.WriteString(`\\`)
		case '"':
			builder.WriteString(`\"`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			builder.WriteRune(runeValue)
		}
	}
	builder.WriteByte('"')

	return builder.String()
}
