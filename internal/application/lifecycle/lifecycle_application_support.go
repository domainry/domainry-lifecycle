package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodulehost "github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
	lifecyclepolicy "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/service"
)

func (s *LifecycleApplicationService) policyExecutor(ctx context.Context, workspaceID, policyKey string, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.PolicyVersion, lifecyclecontract.OwnerLifecycleExecutor, error) {
	version, found, err := s.policies.LatestPolicy(ctx, workspaceID, policyKey, filter)
	if err != nil || !found {
		return lifecyclemodel.PolicyVersion{}, nil, fmt.Errorf("lifecycle policy not found: %s", policyKey)
	}
	executor := s.executors[version.Policy.Owner]
	if executor == nil {
		return lifecyclemodel.PolicyVersion{}, nil, fmt.Errorf("lifecycle owner executor unavailable: %s", version.Policy.Owner)
	}
	return version, executor, nil
}

func (s *LifecycleApplicationService) subjectRequest(ctx context.Context, workspaceID, requestID string, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.SubjectRequest, error) {
	request, found, err := s.subjectRequests.GetSubjectRequest(ctx, workspaceID, requestID, filter)
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

func (s *LifecycleApplicationService) transitionSubject(ctx context.Context, current, next lifecyclemodel.SubjectRequest, actor, event string, filter lifecyclepersistence.DataScopeFilter) (lifecyclemodel.SubjectRequest, error) {
	return s.transitionSubjectWith(ctx, current, next, actor, event, filter, nil)
}

func (s *LifecycleApplicationService) transitionSubjectWith(ctx context.Context, current, next lifecyclemodel.SubjectRequest, actor, event string, filter lifecyclepersistence.DataScopeFilter, before func(context.Context) error) (lifecyclemodel.SubjectRequest, error) {
	err := s.withinTransaction(ctx, func(transactionContext context.Context) error {
		persisted, found, err := s.subjectRequests.GetSubjectRequest(transactionContext, next.WorkspaceID, next.ID, filter)
		if err != nil {
			return err
		}
		if !found || persisted.Status != current.Status || !persisted.UpdatedAt.Equal(current.UpdatedAt) {
			return fmt.Errorf("subject request not found")
		}
		if err := lifecyclepolicy.TransitionSubjectRequest(persisted, next); err != nil {
			return err
		}
		if before != nil {
			if err := before(transactionContext); err != nil {
				return err
			}
		}
		if err := s.subjectRequests.TransitionSubjectRequest(transactionContext, persisted, next, filter); err != nil {
			return err
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

// lifecycleDataScope compiles Identity's exact-Permission data_scope into a
// concrete, internal repository filter. Lifecycle never persists or authors a
// second role/data-scope contract.
func lifecycleDataScope(ctx context.Context, principal lifecycleaccess.Principal, permission string) (lifecyclepersistence.DataScopeFilter, error) {
	if err := lifecycleAuthorize(principal, permission); err != nil {
		return lifecyclepersistence.DataScopeFilter{}, err
	}
	identityPrincipal, known := identitysdk.PrincipalFromContext(ctx)
	if !known || !identityPrincipal.Known || identityPrincipal.UserID != principal.UserID || identityPrincipal.WorkspaceID != principal.WorkspaceID || !identityPrincipal.HasPermission(permission) || identityPrincipal.AccessBundle == nil {
		return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("auth.permission_denied")
	}
	separator := strings.LastIndex(permission, ".")
	if separator <= 0 || separator == len(permission)-1 {
		return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("auth.permission_denied")
	}
	bundle := identityPrincipal.AccessBundle
	if strings.TrimSpace(string(bundle.Subject.WorkspaceID)) != strings.TrimSpace(principal.WorkspaceID) || strings.TrimSpace(string(bundle.Subject.SubjectID)) != strings.TrimSpace(principal.UserID) {
		return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("auth.permission_denied")
	}
	filter := lifecyclepersistence.DataScopeFilter{SubjectOrgID: bundle.Subject.OrgID}
	matched := false
	for _, policy := range bundle.DataPolicies {
		if strings.TrimSpace(string(policy.Resource)) != permission[:separator] || strings.TrimSpace(string(policy.Action)) != permission[separator+1:] {
			continue
		}
		if policy.Effect != identitysdk.EffectAllow || len(policy.DataScopes) == 0 {
			return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("auth.permission_denied")
		}
		matched = true
		for _, scope := range policy.DataScopes {
			switch scope {
			case identitysdk.DataScopeAll:
				if len(policy.DataScopes) != 1 {
					return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("auth.permission_denied")
				}
				filter.Unrestricted = true
			case identitysdk.DataScopeOwner:
				filter.OwnerUserIDs = append(filter.OwnerUserIDs, principal.UserID)
			case identitysdk.DataScopeOrg:
				filter.OwnerOrgIDs = append(filter.OwnerOrgIDs, bundle.Subject.OrgID)
			case identitysdk.DataScopeOrgChild:
				filter.OwnerOrgIDs = append(filter.OwnerOrgIDs, bundle.Subject.OrgScopeIDs...)
			case identitysdk.DataScopeTargetOrg:
				filter.OwnerOrgIDs = append(filter.OwnerOrgIDs, bundle.Subject.SupportOrgScopeIDs...)
			default:
				return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("auth.permission_denied")
			}
		}
	}
	if !matched {
		return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("auth.permission_denied")
	}
	return filter.Normalized(), nil
}

func lifecycleWorkspaceDataScope(ctx context.Context, principal lifecycleaccess.Principal, workspaceID, permission string) (lifecyclepersistence.DataScopeFilter, error) {
	if strings.TrimSpace(principal.WorkspaceID) != strings.TrimSpace(workspaceID) {
		return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("lifecycle workspace scope mismatch")
	}
	return lifecycleDataScope(ctx, principal, permission)
}

func lifecycleCreationDataScope(ctx context.Context, principal lifecycleaccess.Principal, workspaceID, permission string) (lifecyclepersistence.DataScopeFilter, error) {
	filter, err := lifecycleWorkspaceDataScope(ctx, principal, workspaceID, permission)
	if err != nil || !filter.Allows(principal.UserID, filter.SubjectOrgID) {
		return lifecyclepersistence.DataScopeFilter{}, fmt.Errorf("auth.permission_denied")
	}
	return filter, nil
}

func lifecycleWorkspaceOrSystemDataScope(ctx context.Context, principal lifecycleaccess.Principal, workspaceID, permission string) (lifecyclepersistence.DataScopeFilter, error) {
	if principal.Known {
		if _, err := lifecycleaccess.NewSystemCommandScope(principal.SystemScope); err == nil {
			return lifecyclepersistence.UnrestrictedDataScopeFilter(), nil
		}
	}
	return lifecycleWorkspaceDataScope(ctx, principal, workspaceID, permission)
}

func lifecycleAuthorizeSystem(principal lifecycleaccess.Principal) error {
	if !principal.Known {
		return fmt.Errorf("auth.permission_denied")
	}
	if _, err := lifecycleaccess.NewSystemCommandScope(principal.SystemScope); err != nil {
		return fmt.Errorf("auth.permission_denied")
	}
	return nil
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
