package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
)

// WorkerRunner owns one complete Lifecycle maintenance tick. The host owns the
// process loop and cancellation; it does not coordinate Lifecycle use cases.
type WorkerRunner struct {
	service *LifecycleApplicationService
}

type WorkerTick struct {
	LeaseOwner string
	BatchSize  int
	JobLimit   int
	Now        time.Time
	Scope      lifecycleaccess.SystemScope
}

type WorkerTickResult struct {
	ProcessedJobs    int
	DeletedArtifacts int
}

func NewWorkerRunner(service *LifecycleApplicationService) *WorkerRunner {
	return &WorkerRunner{service: service}
}

func (r *WorkerRunner) Tick(ctx context.Context, tick WorkerTick) (WorkerTickResult, error) {
	if r == nil || r.service == nil {
		return WorkerTickResult{}, fmt.Errorf("lifecycle worker service unavailable")
	}
	processed, err := r.service.ProcessRunnableCleanupJobs(ctx, tick.LeaseOwner, tick.BatchSize, tick.JobLimit, tick.Now, tick.Scope)
	deleted, cleanupErr := r.service.CleanupExpiredSubjectArtifacts(ctx, tick.Now, tick.Scope)
	return WorkerTickResult{ProcessedJobs: processed, DeletedArtifacts: deleted}, errors.Join(err, cleanupErr)
}
