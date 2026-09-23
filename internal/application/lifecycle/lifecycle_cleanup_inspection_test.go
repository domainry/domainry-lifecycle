package lifecycle

import (
	"context"
	"testing"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

type cleanupInspectionRepository struct {
	jobs map[string]lifecyclemodel.CleanupJob
}

func (r *cleanupInspectionRepository) SaveCleanupJob(_ context.Context, job lifecyclemodel.CleanupJob) error {
	r.jobs[job.WorkspaceID+":"+job.ID] = job
	return nil
}

func (r *cleanupInspectionRepository) GetCleanupJob(_ context.Context, workspaceID, jobID string) (lifecyclemodel.CleanupJob, bool, error) {
	job, found := r.jobs[workspaceID+":"+jobID]
	return job, found, nil
}

func (*cleanupInspectionRepository) ListRunnableCleanupJobs(context.Context, lifecycleaccess.SystemScope, int, time.Time) ([]lifecyclemodel.CleanupJob, error) {
	return nil, nil
}

func (*cleanupInspectionRepository) ClaimCleanupJob(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error) {
	return lifecyclemodel.CleanupJob{}, false, nil
}

func (*cleanupInspectionRepository) UpdateCleanupJob(context.Context, lifecyclemodel.CleanupJob) error {
	return nil
}

func TestInspectCleanupJobReadsCurrentOwnerStateWithoutMutation(t *testing.T) {
	repository := &cleanupInspectionRepository{jobs: map[string]lifecyclemodel.CleanupJob{
		"workspace-a:job-1": {ID: "job-1", WorkspaceID: "workspace-a", Status: lifecyclemodel.CleanupStatusRunning, Scanned: 10},
	}}
	service := &LifecycleApplicationService{cleanupJobs: repository}
	system := lifecycleaccess.NewSystemPrincipal("runtime-lifecycle-http", lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "inspect cleanup replay readiness"))

	job, err := service.InspectCleanupJob(t.Context(), "workspace-a", "job-1", system)
	if err != nil || job.Scanned != 10 {
		t.Fatalf("first inspection job=%#v err=%v", job, err)
	}
	current := repository.jobs["workspace-a:job-1"]
	current.Scanned = 25
	repository.jobs["workspace-a:job-1"] = current
	job, err = service.InspectCleanupJob(t.Context(), "workspace-a", "job-1", system)
	if err != nil || job.Scanned != 25 {
		t.Fatalf("current inspection job=%#v err=%v", job, err)
	}
	if _, err := service.InspectCleanupJob(t.Context(), "workspace-a", "job-1", lifecycleaccess.Principal{Known: true}); err == nil {
		t.Fatal("non-system cleanup inspection was authorized")
	}
	if _, err := service.InspectCleanupJob(t.Context(), "workspace-a", "missing", system); err == nil {
		t.Fatal("missing cleanup job was returned as ready")
	}
}
