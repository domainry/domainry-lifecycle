package lifecycle

import (
	"context"

	sdkapplication "github.com/domainry/domainry-lifecycle-sdk/application"
)

type (
	Dependencies = sdkapplication.LifecycleApplicationDependencies
	Service      = sdkapplication.LifecycleApplicationService
	WorkerRunner = sdkapplication.WorkerRunner
	WorkerTick   = sdkapplication.WorkerTick
	WorkerResult = sdkapplication.WorkerTickResult
)

const (
	PermissionPolicyManage  = sdkapplication.PermissionPolicyManage
	PermissionCleanupRun    = sdkapplication.PermissionCleanupRun
	PermissionSubjectManage = sdkapplication.PermissionSubjectManage
)

func NewService(ctx context.Context, dependencies Dependencies) *Service {
	return sdkapplication.NewLifecycleApplicationService(ctx, dependencies)
}

func NewWorkerRunner(service *Service) *WorkerRunner {
	return sdkapplication.NewWorkerRunner(service)
}
