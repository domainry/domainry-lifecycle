package lifecycle

import (
	"context"
	"fmt"
	"time"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
)

func (s *LifecycleApplicationService) ProcessRunnableCleanupJobs(ctx context.Context, leaseOwner string, batchSize, jobLimit int, now time.Time, scope lifecycleaccess.SystemScope) (int, error) {
	principal := lifecycleaccess.NewSystemPrincipal(leaseOwner, scope)
	jobs, err := s.cleanupJobs.ListRunnableCleanupJobs(ctx, scope, jobLimit, now)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, job := range jobs {
		if _, err := s.ProcessCleanupJob(ctx, job.WorkspaceID, job.ID, leaseOwner, 2*time.Minute, batchSize, now, principal); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

// ReplayRegisteredDeletions is a restore gate: a restored workspace must run
// its durable deletion registry before it can be exposed for normal traffic.
func (s *LifecycleApplicationService) ReplayRegisteredDeletions(ctx context.Context, workspaceID string, limit int, principal lifecycleaccess.Principal) (int, error) {
	filter, err := lifecycleWorkspaceOrSystemDataScope(ctx, principal, workspaceID, lifecyclesdk.ActionLifecycleDeletionsReplay)
	if err != nil {
		return 0, err
	}
	replayed := 0
	err = s.withinTransaction(ctx, func(transactionContext context.Context) error {
		registrations, err := s.subjectRequests.ListPendingDeletionRegistrations(transactionContext, workspaceID, limit, filter)
		if err != nil {
			return err
		}
		// Validate the complete candidate set before the first owner side effect.
		// Embedded owner handlers receive the same host transaction context and
		// the durable request ID remains their retry/idempotency identity.
		for _, registration := range registrations {
			holds, holdErr := s.legalHolds.ActiveLegalHolds(transactionContext, lifecyclemodel.ResourceTarget{WorkspaceID: workspaceID, ResourceType: "data_subject", ResourceID: registration.ResolvedIdentity}, time.Now().UTC())
			if holdErr != nil {
				return holdErr
			}
			if len(holds) > 0 {
				return fmt.Errorf("backup deletion replay blocked by legal hold")
			}
		}
		for _, registration := range registrations {
			for _, handler := range s.subjectHandlers {
				if _, err := handler.EraseSubjectForRequest(transactionContext, registration.RequestID, workspaceID, registration.ResolvedIdentity, nil); err != nil {
					return err
				}
			}
			if err := s.audit(transactionContext, workspaceID, "lifecycle.deletion.replayed", principal.UserID, registration.RequestID, "", registration); err != nil {
				return err
			}
			replayed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return replayed, nil
}

func (s *LifecycleApplicationService) CleanupExpiredSubjectArtifacts(ctx context.Context, now time.Time, scope lifecycleaccess.SystemScope) (int, error) {
	if _, err := lifecycleaccess.NewSystemCommandScope(scope); err != nil {
		return 0, err
	}
	if s.artifacts == nil {
		return 0, nil
	}
	deleted, err := s.artifacts.DeleteExpiredSubjectExports(ctx, now)
	if err != nil {
		return deleted, err
	}
	stagingDeleted, err := s.artifacts.DeleteExpiredUploadStaging(ctx, now)
	deleted += stagingDeleted
	if err != nil {
		return deleted, err
	}
	if s.uploadArtifacts != nil {
		result, reconcileErr := s.uploadArtifacts.ReconcileUploadArtifacts(ctx, scope, now, 500)
		deleted += result.Deleted
		if reconcileErr != nil {
			return deleted, reconcileErr
		}
	}
	expired, err := s.subjectRequests.ExpireSubjectExportReferences(ctx, scope, now)
	if err != nil {
		return deleted, err
	}
	for _, request := range expired {
		if err := s.audit(ctx, request.WorkspaceID, "lifecycle.subject.export_expired", "lifecycle_cleanup", request.ID, "", map[string]any{"request_id": request.ID}); err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
