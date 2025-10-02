package runner

import (
	"context"
	"os"
	"runner/temporal/utils"
)

type ResultatOchistkiDockera struct {
	Success bool
}

func OchistkaDockeraActivity(ctx context.Context) (*ResultatOchistkiDockera, error) {
	result := &ResultatOchistkiDockera{
		Success: true,
	}

	currentPath, err := os.Getwd()

	if err != nil {
		result.Success = false
		return result, nil
	}

	args := []string{"docker", "system", "prune", "-af", "--volumes"}
	_, err = utils.ExecCommand(currentPath, args)

	if err != nil {
		result.Success = false
	}

	return result, nil
}
