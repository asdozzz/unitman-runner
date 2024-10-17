package runner

import (
	"context"
	"os"
	"runner/temporal/utils"
)

type RunnerHealthCheckResult struct {
	Success     bool
	DockerStats string
	MemInfo     string
}

func RunnerHealthCheckActivity(ctx context.Context) (*RunnerHealthCheckResult, error) {
	result := &RunnerHealthCheckResult{
		Success:     true,
		DockerStats: "",
		MemInfo:     "",
	}

	currentPath, err := os.Getwd()

	if err != nil {
		return result, nil
	}
	args := []string{"cat", "/proc/meminfo"}
	msg, err := utils.ExecCommand(currentPath, args)

	if err == nil {
		result.MemInfo = msg
	}

	args = []string{"docker", "stats", "--no-stream", "--format", "json"}
	msg, err = utils.ExecCommand(currentPath, args)

	if err == nil {
		result.DockerStats = msg
	}

	return result, nil
}
