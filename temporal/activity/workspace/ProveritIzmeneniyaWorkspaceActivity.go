package workspace

import (
	"context"
	"strings"
)

// ProveritIzmeneniyaWorkspaceActivity проверяет незакоммиченные изменения
// в каждом проекте через `git status --porcelain`.
func ProveritIzmeneniyaWorkspaceActivity(
	ctx context.Context,
	command ProveritIzmeneniyaWorkspaceCommand,
) (*ResultatProverkiWorkspace, error) {
	result := &ResultatProverkiWorkspace{
		Success:  true,
		Projects: []ProjectIzmeneniya{},
	}

	if err := validateWorkspaceID(command.WorkspaceId); err != nil {
		result.Success = false
		return result, nil
	}

	workspaceDir := workspaceDirectory(command.WorkspaceId)

	for _, project := range command.Projects {
		dirName := projectDirName(project.ProjectId)
		if dirName == "" {
			result.Success = false
			continue
		}

		commandLine := "git -C " + shellQuote(workspaceContainerDir+"/"+dirName) + " status --porcelain"
		output, err := execWorkspace(ctx, workspaceDir, commandLine, workspaceContainerDir, nil)

		files := parsePorcelain(output)
		if err != nil {
			result.Success = false
		}

		result.Projects = append(result.Projects, ProjectIzmeneniya{
			ProjectId: project.ProjectId,
			Name:      project.ProjectName,
			Dirty:     len(files) > 0,
			Files:     files,
		})
	}

	return result, nil
}

// parsePorcelain разбирает вывод git status --porcelain в список строк.
func parsePorcelain(output string) []string {
	files := []string{}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		files = append(files, strings.TrimRight(line, "\r"))
	}
	return files
}
