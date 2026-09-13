package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/domainry/domainry-foundation/requestcontext"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"strings"
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
	ctx = requestcontext.WithWorkspaceID(ctx, workspaceID)
	registrations, err := s.subjectRequests.ListPendingDeletionRegistrations(ctx, workspaceID, limit, filter)
	if err != nil {
		return 0, err
	}
	// The restore gate keeps traffic closed. Validate holds for the full batch
	// before starting source-owned transactions or external file effects.
	for _, registration := range registrations {
		holds, err := s.legalHolds.ActiveLegalHolds(ctx, lifecyclemodel.ResourceTarget{WorkspaceID: workspaceID, ResourceType: "data_subject", ResourceID: registration.ResolvedIdentity}, time.Now().UTC())
		if err != nil {
			return 0, err
		}
		if len(holds) > 0 {
			return 0, fmt.Errorf("backup deletion replay blocked by legal hold")
		}
	}
	replayed := 0
	for _, registration := range registrations {
		steps, err := s.subjectRequests.ListSubjectExecutionSteps(ctx, workspaceID, registration.RequestID)
		if err != nil {
			return replayed, err
		}
		plans := map[string]json.RawMessage{}
		for _, step := range steps {
			if step.Operation == "erase_plan" {
				plans[step.Owner] = step.Payload
			}
		}
		for _, handler := range s.subjectHandlers {
			planner, ok := handler.(lifecyclecontract.PreparedSubjectErasureHandler)
			if !ok {
				continue
			}
			owner := strings.TrimSpace(handler.Owner(ctx))
			if _, exists := plans[owner]; exists {
				continue
			}
			plan, err := planner.PrepareSubjectErasure(ctx, registration.RequestID, workspaceID, registration.ResolvedIdentity)
			if err != nil {
				return replayed, err
			}
			if !json.Valid(plan) {
				return replayed, fmt.Errorf("invalid restore erasure plan")
			}
			request := lifecyclemodel.SubjectRequest{WorkspaceID: workspaceID, ID: registration.RequestID}
			if err = s.saveSubjectExecutionStep(ctx, request, owner, "erase_plan", plan, time.Now().UTC()); err != nil {
				return replayed, err
			}
			plans[owner] = plan
		}
		for _, handler := range s.subjectHandlers {
			if planner, ok := handler.(lifecyclecontract.PreparedSubjectErasureHandler); ok {
				_, err = planner.ErasePreparedSubject(ctx, registration.RequestID, workspaceID, registration.ResolvedIdentity, plans[strings.TrimSpace(handler.Owner(ctx))], nil)
			} else {
				_, err = handler.EraseSubjectForRequest(ctx, registration.RequestID, workspaceID, registration.ResolvedIdentity, nil)
			}
			if err != nil {
				return replayed, err
			}
		}
		if err = s.withinTransaction(ctx, func(txctx context.Context) error {
			return s.audit(txctx, workspaceID, "lifecycle.deletion.replayed", principal.UserID, registration.RequestID, "", registration)
		}); err != nil {
			return replayed, err
		}
		replayed++
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
