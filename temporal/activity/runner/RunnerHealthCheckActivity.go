package runner

import (
	"context"
)

type RunnerHealthCheckResult struct {
	Success bool
}

func RunnerHealthCheckActivity(ctx context.Context) (*RunnerHealthCheckResult, error) {
	result := &RunnerHealthCheckResult{
		Success: true,
	}

	return result, nil
}
