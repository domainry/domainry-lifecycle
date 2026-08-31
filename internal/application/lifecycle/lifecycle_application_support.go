package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodulehost "github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
	lifecyclepolicy "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/service"
)

func (s *LifecycleApplicationService) policyExecutor(ctx context.Context, workspaceID, policyKey string) (lifecyclemodel.PolicyVersion, lifecyclecontract.OwnerLifecycleExecutor, error) {
	version, found, err := s.policies.LatestPolicy(ctx, workspaceID, policyKey)
	if err != nil || !found {
		return lifecyclemodel.PolicyVersion{}, nil, fmt.Errorf("lifecycle policy not found: %s", policyKey)
	}
	executor := s.executors[version.Policy.Owner]
	if executor == nil {
		return lifecyclemodel.PolicyVersion{}, nil, fmt.Errorf("lifecycle owner executor unavailable: %s", version.Policy.Owner)
	}
	return version, executor, nil
}

func (s *LifecycleApplicationService) subjectRequest(ctx context.Context, workspaceID, requestID string) (lifecyclemodel.SubjectRequest, error) {
	request, found, err := s.subjectRequests.GetSubjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	if !found {
		return lifecyclemodel.SubjectRequest{}, fmt.Errorf("subject request not found")
	}
	return request, nil
}

func subjectExecutionStepKey(owner, operation string) string {
	return strings.TrimSpace(owner) + "\x00" + strings.TrimSpace(operation)
}

func (s *LifecycleApplicationService) saveSubjectExecutionStep(ctx context.Context, request lifecyclemodel.SubjectRequest, owner, operation string, payload json.RawMessage, completedAt time.Time) error {
	return s.subjectRequests.SaveSubjectExecutionStep(ctx, lifecyclemodel.SubjectExecutionStep{
		WorkspaceID: request.WorkspaceID, RequestID: request.ID, Owner: strings.TrimSpace(owner),
		Operation: strings.TrimSpace(operation), Payload: append(json.RawMessage(nil), payload...), CompletedAt: completedAt,
	})
}

func (s *LifecycleApplicationService) transitionSubject(ctx context.Context, current, next lifecyclemodel.SubjectRequest, actor, event string) (lifecyclemodel.SubjectRequest, error) {
	return s.transitionSubjectWith(ctx, current, next, actor, event, nil)
}

func (s *LifecycleApplicationService) transitionSubjectWith(ctx context.Context, current, next lifecyclemodel.SubjectRequest, actor, event string, before func(context.Context) error) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecyclepolicy.TransitionSubjectRequest(current, next); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	err := s.withinTransaction(ctx, func(transactionContext context.Context) error {
		if before != nil {
			if err := before(transactionContext); err != nil {
				return err
			}
		}
		var transitionErr error
		if transitions, ok := s.subjectRequests.(lifecyclepersistence.SubjectRequestTransitionRepository); ok {
			transitionErr = transitions.TransitionSubjectRequest(transactionContext, current, next)
		} else {
			transitionErr = s.subjectRequests.SaveSubjectRequest(transactionContext, next)
		}
		if transitionErr != nil {
			return transitionErr
		}
		return s.audit(transactionContext, next.WorkspaceID, event, actor, next.ID, "", next)
	})
	return next, err
}

func (s *LifecycleApplicationService) failCleanup(ctx context.Context, job lifecyclemodel.CleanupJob, cause error, now time.Time, actor string) (lifecyclemodel.CleanupJob, error) {
	job.Status, job.LastError, job.LeaseOwner, job.LeaseExpiresAt, job.UpdatedAt = lifecyclemodel.CleanupStatusFailed, cause.Error(), "", time.Time{}, now
	if err := s.withinTransaction(ctx, func(transactionContext context.Context) error {
		if err := s.cleanupJobs.UpdateCleanupJob(transactionContext, job); err != nil {
			return err
		}
		return s.audit(transactionContext, job.WorkspaceID, "lifecycle.cleanup.failed", actor, job.ID, job.PolicyKey, job)
	}); err != nil {
		return job, err
	}
	return job, cause
}

func (s *LifecycleApplicationService) audit(ctx context.Context, workspaceID, event, actor, resourceID, policyKey string, payload any) error {
	raw, _ := json.Marshal(payload)
	return s.evidence.AppendAuditEvidence(ctx, lifecyclemodel.AuditEvidence{ID: requestcontext.NewRequestID(), WorkspaceID: workspaceID, Event: event, ActorID: actor, ResourceID: resourceID, PolicyKey: policyKey, Payload: raw, CreatedAt: time.Now().UTC()})
}

func lifecycleAuthorize(principal lifecycleaccess.Principal, permission string) error {
	if _, err := lifecycleaccess.NewWorkspaceID(principal.WorkspaceID); !principal.Known || err != nil || !principal.HasPermission(permission) {
		return fmt.Errorf("auth.permission_denied")
	}
	return nil
}

func lifecycleAuthorizeWorkspace(principal lifecycleaccess.Principal, workspaceID, permission string) error {
	if err := lifecycleAuthorize(principal, permission); err != nil {
		return err
	}
	if strings.TrimSpace(principal.WorkspaceID) != strings.TrimSpace(workspaceID) {
		return fmt.Errorf("lifecycle workspace scope mismatch")
	}
	return nil
}

func lifecycleAuthorizeWorkspaceOrSystem(principal lifecycleaccess.Principal, workspaceID, permission string) error {
	if principal.Known && principal.SystemScope.Valid() {
		return nil
	}
	return lifecycleAuthorizeWorkspace(principal, workspaceID, permission)
}

func convertValue[T any](value any) (T, error) {
	var result T
	payload, err := json.Marshal(value)
	if err != nil {
		return result, fmt.Errorf("encode Lifecycle boundary value: %w", err)
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return result, fmt.Errorf("decode Lifecycle boundary value: %w", err)
	}
	return result, nil
}

func (s *LifecycleApplicationService) withinTransaction(ctx context.Context, operation func(context.Context) error) error {
	if s == nil || s.transactions == nil {
		return operation(ctx)
	}
	return s.transactions.WithinTransaction(ctx, func(transactionContext context.Context, _ lifecyclemodulehost.DBTX) error {
		return operation(transactionContext)
	})
}
