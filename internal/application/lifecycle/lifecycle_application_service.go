package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	sdkmodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
	lifecyclepolicy "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/service"
)

type LifecycleApplicationDependencies struct {
	Policies        lifecyclepersistence.PolicyRepository
	LegalHolds      lifecyclepersistence.LegalHoldRepository
	CleanupJobs     lifecyclepersistence.CleanupJobRepository
	SubjectRequests lifecyclepersistence.SubjectRequestRepository
	Evidence        lifecyclepersistence.LifecycleEvidenceRepository
	Executors       []lifecyclecontract.OwnerLifecycleExecutor
	SubjectResolver lifecyclecontract.SubjectIdentityResolver
	SubjectHandlers []lifecyclecontract.SubjectExecutionHandler
	ExternalErasure lifecyclecontract.ExternalErasureHandler
	Artifacts       lifecyclecontract.SubjectArtifactStore
	UploadArtifacts lifecyclecontract.UploadArtifactStore
	Transactions    modulehost.Transactor
}

type LifecycleApplicationService struct {
	policies        lifecyclepersistence.PolicyRepository
	legalHolds      lifecyclepersistence.LegalHoldRepository
	cleanupJobs     lifecyclepersistence.CleanupJobRepository
	subjectRequests lifecyclepersistence.SubjectRequestRepository
	evidence        lifecyclepersistence.LifecycleEvidenceRepository
	executors       map[string]lifecyclecontract.OwnerLifecycleExecutor
	resolver        lifecyclecontract.SubjectIdentityResolver
	subjectHandlers []lifecyclecontract.SubjectExecutionHandler
	externalErasure lifecyclecontract.ExternalErasureHandler
	artifacts       lifecyclecontract.SubjectArtifactStore
	uploadArtifacts lifecyclecontract.UploadArtifactStore
	transactions    modulehost.Transactor
}

func NewLifecycleApplicationService(ctx context.Context, deps LifecycleApplicationDependencies) *LifecycleApplicationService {
	service := &LifecycleApplicationService{policies: deps.Policies, legalHolds: deps.LegalHolds, cleanupJobs: deps.CleanupJobs, subjectRequests: deps.SubjectRequests, evidence: deps.Evidence, executors: map[string]lifecyclecontract.OwnerLifecycleExecutor{}, resolver: deps.SubjectResolver, subjectHandlers: append([]lifecyclecontract.SubjectExecutionHandler(nil), deps.SubjectHandlers...), externalErasure: deps.ExternalErasure, artifacts: deps.Artifacts, uploadArtifacts: deps.UploadArtifacts, transactions: deps.Transactions}
	for _, executor := range deps.Executors {
		if executor != nil && strings.TrimSpace(executor.Owner(ctx)) != "" {
			service.executors[strings.TrimSpace(executor.Owner(ctx))] = executor
		}
	}
	return service
}

func (s *LifecycleApplicationService) PublishPolicy(ctx context.Context, version lifecyclemodel.PolicyVersion, principal lifecycleaccess.Principal) (lifecyclemodel.PolicyVersion, error) {
	if err := lifecycleAuthorize(principal, lifecyclesdk.ActionLifecyclePoliciesPublish); err != nil {
		return lifecyclemodel.PolicyVersion{}, err
	}
	if s == nil || s.policies == nil {
		return lifecyclemodel.PolicyVersion{}, fmt.Errorf("lifecycle repository unavailable")
	}
	version.PublishedBy = principal.UserID
	version.WorkspaceID = principal.WorkspaceID
	if version.Status == "" {
		version.Status = lifecyclemodel.PolicyStatusPublished
	}
	if version.PublishedAt.IsZero() {
		version.PublishedAt = time.Now().UTC()
	}
	previous, found, err := s.policies.LatestPolicy(ctx, principal.WorkspaceID, version.Policy.Key)
	if err != nil {
		return lifecyclemodel.PolicyVersion{}, err
	}
	if found {
		if version.Revision <= 0 {
			version.Revision = previous.Revision + 1
		}
		if err := lifecyclepolicy.ValidatePolicyPublication(&previous, version); err != nil {
			return lifecyclemodel.PolicyVersion{}, err
		}
	} else {
		if version.Revision <= 0 {
			version.Revision = 1
		}
		if err := lifecyclepolicy.ValidatePolicyPublication(nil, version); err != nil {
			return lifecyclemodel.PolicyVersion{}, err
		}
	}
	err = s.withinTransaction(ctx, func(transactionContext context.Context) error {
		if err := s.policies.SavePolicy(transactionContext, version); err != nil {
			return err
		}
		return s.audit(transactionContext, principal.WorkspaceID, "lifecycle.policy.published", principal.UserID, version.Policy.Key, version.Policy.Key, version)
	})
	return version, err
}

func (s *LifecycleApplicationService) ListPolicies(ctx context.Context, principal lifecycleaccess.Principal) ([]lifecyclemodel.PolicyVersion, error) {
	if err := lifecycleAuthorize(principal, lifecyclesdk.ActionLifecyclePoliciesList); err != nil {
		return nil, err
	}
	return s.policies.ListPolicies(ctx, principal.WorkspaceID)
}

func (s *LifecycleApplicationService) CreateLegalHold(ctx context.Context, hold lifecyclemodel.LegalHold, principal lifecycleaccess.Principal) (lifecyclemodel.LegalHold, error) {
	if err := lifecycleAuthorize(principal, lifecyclesdk.ActionLifecycleLegalHoldsCreate); err != nil {
		return lifecyclemodel.LegalHold{}, err
	}
	if hold.WorkspaceID != principal.WorkspaceID {
		return lifecyclemodel.LegalHold{}, fmt.Errorf("lifecycle workspace scope mismatch")
	}
	if hold.ID == "" {
		hold.ID = requestcontext.NewRequestID()
	}
	if err := lifecyclepolicy.ValidateLegalHold(hold); err != nil {
		return lifecyclemodel.LegalHold{}, err
	}
	err := s.withinTransaction(ctx, func(transactionContext context.Context) error {
		if err := s.legalHolds.SaveLegalHold(transactionContext, hold); err != nil {
			return err
		}
		return s.audit(transactionContext, hold.WorkspaceID, "lifecycle.legal_hold.created", principal.UserID, hold.ID, "", hold)
	})
	return hold, err
}

func (s *LifecycleApplicationService) EndLegalHold(ctx context.Context, workspaceID, holdID, authority, evidence string, endedAt time.Time, principal lifecycleaccess.Principal) (lifecyclemodel.LegalHold, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, lifecyclesdk.ActionLifecycleLegalHoldsEnd); err != nil {
		return lifecyclemodel.LegalHold{}, err
	}
	hold, found, err := s.legalHolds.GetLegalHold(ctx, workspaceID, holdID)
	if err != nil || !found {
		return lifecyclemodel.LegalHold{}, fmt.Errorf("legal hold not found")
	}
	if strings.TrimSpace(authority) == "" || strings.TrimSpace(evidence) == "" || endedAt.IsZero() || !endedAt.After(hold.StartsAt) {
		return lifecyclemodel.LegalHold{}, fmt.Errorf("legal hold release authority, evidence and valid end are required")
	}
	hold.Authority, hold.AuditEvidence, hold.EndsAt = strings.TrimSpace(authority), strings.TrimSpace(evidence), &endedAt
	err = s.withinTransaction(ctx, func(transactionContext context.Context) error {
		if err := s.legalHolds.SaveLegalHold(transactionContext, hold); err != nil {
			return err
		}
		return s.audit(transactionContext, workspaceID, "lifecycle.legal_hold.ended", principal.UserID, hold.ID, "", hold)
	})
	return hold, err
}

func (s *LifecycleApplicationService) PreviewCleanup(ctx context.Context, workspaceID, policyKey string, principal lifecycleaccess.Principal, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, lifecyclesdk.ActionLifecycleCleanupPreview); err != nil {
		return lifecyclecontract.CleanupPreview{}, err
	}
	policyVersion, executor, err := s.policyExecutor(ctx, workspaceID, policyKey)
	if err != nil {
		return lifecyclecontract.CleanupPreview{}, err
	}
	externalPolicy, err := convertValue[sdkmodel.PolicyVersion](policyVersion)
	if err != nil {
		return lifecyclecontract.CleanupPreview{}, err
	}
	return executor.Preview(ctx, workspaceID, externalPolicy, now)
}

func (s *LifecycleApplicationService) CreateCleanupJob(ctx context.Context, job lifecyclemodel.CleanupJob, principal lifecycleaccess.Principal) (lifecyclemodel.CleanupJob, error) {
	if err := lifecycleAuthorizeWorkspace(principal, job.WorkspaceID, lifecyclesdk.ActionLifecycleCleanupJobsCreate); err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	policyVersion, executor, err := s.policyExecutor(ctx, job.WorkspaceID, job.PolicyKey)
	if err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	if job.ID == "" {
		job.ID = requestcontext.NewRequestID()
	}
	job.PolicyVersion = policyVersion.Policy.Version
	job.RequestedBy = principal.UserID
	if job.Status == "" {
		job.Status = lifecyclemodel.CleanupStatusPending
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	job.UpdatedAt = job.CreatedAt
	externalPolicy, err := convertValue[sdkmodel.PolicyVersion](policyVersion)
	if err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	preview, err := executor.Preview(ctx, job.WorkspaceID, externalPolicy, job.CreatedAt)
	if err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	job.EstimatedRows, job.EstimatedBytes, job.OldestEligible = preview.Rows, preview.Bytes, preview.OldestEligible
	if err := lifecyclepolicy.ValidateCleanupJob(job); err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	err = s.withinTransaction(ctx, func(transactionContext context.Context) error {
		if err := s.cleanupJobs.SaveCleanupJob(transactionContext, job); err != nil {
			return err
		}
		return s.audit(transactionContext, job.WorkspaceID, "lifecycle.cleanup.created", principal.UserID, job.ID, job.PolicyKey, job)
	})
	return job, err
}

func (s *LifecycleApplicationService) ProcessCleanupJob(ctx context.Context, workspaceID, jobID, leaseOwner string, leaseTTL time.Duration, batchSize int, now time.Time, principal lifecycleaccess.Principal) (lifecyclemodel.CleanupJob, error) {
	if err := lifecycleAuthorizeSystem(principal); err != nil {
		return lifecyclemodel.CleanupJob{}, err
	}
	if batchSize <= 0 || batchSize > 1000 {
		batchSize = 100
	}
	claimed, acquired, err := s.cleanupJobs.ClaimCleanupJob(ctx, workspaceID, jobID, leaseOwner, leaseTTL, now)
	if err != nil || !acquired {
		return claimed, err
	}
	policyVersion, executor, err := s.policyExecutor(ctx, workspaceID, claimed.PolicyKey)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	holds, err := s.legalHolds.ActiveLegalHolds(ctx, lifecyclemodel.ResourceTarget{WorkspaceID: workspaceID, Owner: policyVersion.Policy.Owner, ResourceID: "*"}, now)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	externalJob, err := convertValue[sdkmodel.CleanupJob](claimed)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	externalPolicy, err := convertValue[sdkmodel.PolicyVersion](policyVersion)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	externalHolds, err := convertValue[[]sdkmodel.LegalHold](holds)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	externalResult, err := executor.ProcessBatch(ctx, externalJob, externalPolicy, externalHolds, batchSize)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	result, err := convertValue[lifecyclemodel.CleanupBatchResult](externalResult)
	if err != nil {
		return s.failCleanup(ctx, claimed, err, now, principal.UserID)
	}
	claimed.Checkpoint = result.Checkpoint
	claimed.Scanned += result.Scanned
	claimed.Archived += result.Archived
	claimed.Purged += result.Purged
	claimed.Skipped += result.Skipped
	claimed.Failed += result.Failed
	claimed.OldestEligible = result.OldestEligible
	claimed.UpdatedAt = now
	claimed.LeaseOwner, claimed.LeaseExpiresAt = "", time.Time{}
	if result.Done || claimed.DryRun {
		claimed.Status = lifecyclemodel.CleanupStatusSucceeded
	} else {
		claimed.Status = lifecyclemodel.CleanupStatusPending
	}
	event := "lifecycle.cleanup.progressed"
	if claimed.Status == lifecyclemodel.CleanupStatusSucceeded {
		event = "lifecycle.cleanup.succeeded"
	}
	err = s.withinTransaction(ctx, func(transactionContext context.Context) error {
		if err := s.cleanupJobs.UpdateCleanupJob(transactionContext, claimed); err != nil {
			return err
		}
		return s.audit(transactionContext, workspaceID, event, principal.UserID, claimed.ID, claimed.PolicyKey, claimed)
	})
	return claimed, err
}

func (s *LifecycleApplicationService) CreateSubjectRequest(ctx context.Context, request lifecyclemodel.SubjectRequest, principal lifecycleaccess.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspace(principal, request.WorkspaceID, lifecyclesdk.ActionLifecycleSubjectRequestsCreate); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	if request.ID == "" {
		request.ID = requestcontext.NewRequestID()
	}
	request.RequestedBy, request.Status = principal.UserID, lifecyclemodel.SubjectRequestPendingVerification
	request.CreatedAt, request.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	if request.Kind != lifecyclemodel.SubjectRequestExport && request.Kind != lifecyclemodel.SubjectRequestErase {
		return lifecyclemodel.SubjectRequest{}, fmt.Errorf("unsupported subject request kind")
	}
	if request.SubjectID == "" || request.SubjectType == "" || request.Reason == "" {
		return lifecyclemodel.SubjectRequest{}, fmt.Errorf("subject identity and reason are required")
	}
	err := s.withinTransaction(ctx, func(transactionContext context.Context) error {
		if err := s.subjectRequests.SaveSubjectRequest(transactionContext, request); err != nil {
			return err
		}
		return s.audit(transactionContext, request.WorkspaceID, "lifecycle.subject.requested", principal.UserID, request.ID, "", request)
	})
	return request, err
}

func (s *LifecycleApplicationService) VerifySubjectRequest(ctx context.Context, workspaceID, requestID, secondFactor string, principal lifecycleaccess.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, lifecyclesdk.ActionLifecycleSubjectRequestsVerify); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	if s.resolver == nil {
		return lifecyclemodel.SubjectRequest{}, fmt.Errorf("subject resolver unavailable")
	}
	resolved, err := s.resolver.ResolveSubject(ctx, workspaceID, request.SubjectType, request.SubjectID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	next := request
	next.Status, next.ResolvedIdentity, next.VerifiedBy, next.SecondFactorRef, next.UpdatedAt = lifecyclemodel.SubjectRequestVerified, resolved, principal.UserID, strings.TrimSpace(secondFactor), time.Now().UTC()
	return s.transitionSubject(ctx, request, next, principal.UserID, "lifecycle.subject.verified")
}

func (s *LifecycleApplicationService) PreviewSubjectRequest(ctx context.Context, workspaceID, requestID string, principal lifecycleaccess.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, lifecyclesdk.ActionLifecycleSubjectRequestsPreview); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	preview := map[string]json.RawMessage{}
	for _, handler := range s.subjectHandlers {
		payload, handlerErr := handler.PreviewSubject(ctx, workspaceID, request.ResolvedIdentity)
		if handlerErr != nil {
			return lifecyclemodel.SubjectRequest{}, handlerErr
		}
		preview[handler.Owner(ctx)] = payload
	}
	raw, _ := json.Marshal(preview)
	next := request
	next.Status, next.ImpactPreview, next.UpdatedAt = lifecyclemodel.SubjectRequestPreviewed, raw, time.Now().UTC()
	return s.transitionSubject(ctx, request, next, principal.UserID, "lifecycle.subject.previewed")
}

func (s *LifecycleApplicationService) ApproveSubjectRequest(ctx context.Context, workspaceID, requestID string, principal lifecycleaccess.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspace(principal, workspaceID, lifecyclesdk.ActionLifecycleSubjectRequestsApprove); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	next := request
	next.Status, next.ApprovedBy, next.UpdatedAt = lifecyclemodel.SubjectRequestApproved, principal.UserID, time.Now().UTC()
	return s.transitionSubject(ctx, request, next, principal.UserID, "lifecycle.subject.approved")
}

func (s *LifecycleApplicationService) ExecuteSubjectRequest(ctx context.Context, workspaceID, requestID string, principal lifecycleaccess.Principal) (lifecyclemodel.SubjectRequest, error) {
	if err := lifecycleAuthorizeWorkspaceOrSystem(principal, workspaceID, lifecyclesdk.ActionLifecycleSubjectRequestsExecute); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	request, err := s.subjectRequest(ctx, workspaceID, requestID)
	if err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	executing := request
	executing.Status, executing.UpdatedAt = lifecyclemodel.SubjectRequestExecuting, time.Now().UTC()
	executing.ExecutionAttempt++
	executing.ExecutionLeaseEnd = executing.UpdatedAt.Add(5 * time.Minute)
	executing.LastError = ""
	if executing, err = s.transitionSubject(ctx, request, executing, principal.UserID, "lifecycle.subject.executing"); err != nil {
		return lifecyclemodel.SubjectRequest{}, err
	}
	result := map[string]json.RawMessage{}
	steps, err := s.subjectRequests.ListSubjectExecutionSteps(ctx, workspaceID, executing.ID)
	completedSteps := make(map[string]json.RawMessage, len(steps))
	for _, step := range steps {
		completedSteps[subjectExecutionStepKey(step.Owner, step.Operation)] = append(json.RawMessage(nil), step.Payload...)
	}
	var holds []lifecyclemodel.LegalHold
	if err == nil {
		holds, err = s.legalHolds.ActiveLegalHolds(ctx, lifecyclemodel.ResourceTarget{WorkspaceID: workspaceID, Owner: "", ResourceType: "data_subject", ResourceID: executing.ResolvedIdentity}, executing.UpdatedAt)
	}
	if err == nil && executing.Kind == lifecyclemodel.SubjectRequestErase && len(holds) > 0 {
		err = fmt.Errorf("subject erasure blocked by legal hold")
	}
	if err == nil && executing.Kind == lifecyclemodel.SubjectRequestErase && s.externalErasure != nil {
		stepKey := subjectExecutionStepKey("external_erasure", "erase")
		var values []lifecyclemodel.ExternalErasure
		if payload, found := completedSteps[stepKey]; found {
			err = json.Unmarshal(payload, &values)
		} else {
			externalRequest, conversionErr := convertValue[sdkmodel.SubjectRequest](executing)
			if conversionErr != nil {
				err = conversionErr
			} else {
				external, externalErr := s.externalErasure.RequestExternalErasure(ctx, externalRequest)
				if externalErr != nil {
					err = externalErr
				} else if values, conversionErr = convertValue[[]lifecyclemodel.ExternalErasure](external); conversionErr != nil {
					err = conversionErr
				}
			}
		}
		if err == nil {
			payload, marshalErr := json.Marshal(values)
			if marshalErr != nil {
				err = marshalErr
			} else {
				err = s.withinTransaction(ctx, func(transactionContext context.Context) error {
					if err := s.subjectRequests.SaveExternalErasures(transactionContext, values); err != nil {
						return err
					}
					return s.saveSubjectExecutionStep(transactionContext, executing, "external_erasure", "erase", payload, time.Now().UTC())
				})
			}
		}
	}
	if err == nil {
		for _, handler := range s.subjectHandlers {
			owner, operation := strings.TrimSpace(handler.Owner(ctx)), string(executing.Kind)
			var payload json.RawMessage
			if previous, found := completedSteps[subjectExecutionStepKey(owner, operation)]; found {
				payload = append(json.RawMessage(nil), previous...)
			} else if executing.Kind == lifecyclemodel.SubjectRequestExport {
				payload, err = handler.ExportSubjectForRequest(ctx, executing.ID, workspaceID, executing.ResolvedIdentity)
			} else {
				externalHolds, conversionErr := convertValue[[]sdkmodel.LegalHold](holds)
				if conversionErr != nil {
					err = conversionErr
					break
				}
				payload, err = handler.EraseSubjectForRequest(ctx, executing.ID, workspaceID, executing.ResolvedIdentity, externalHolds)
			}
			if err != nil {
				break
			}
			if _, found := completedSteps[subjectExecutionStepKey(owner, operation)]; !found {
				err = s.saveSubjectExecutionStep(ctx, executing, owner, operation, payload, time.Now().UTC())
				if err != nil {
					break
				}
			}
			result[owner] = payload
		}
	}
	completed := executing
	completed.UpdatedAt = time.Now().UTC()
	completed.ExecutionLeaseEnd = time.Time{}
	if err != nil {
		completed.Status, completed.LastError = lifecyclemodel.SubjectRequestFailed, err.Error()
		return s.transitionSubject(ctx, executing, completed, principal.UserID, "lifecycle.subject.failed")
	}
	raw, _ := json.Marshal(result)
	if completed.Kind == lifecyclemodel.SubjectRequestExport {
		if s.artifacts == nil {
			err = fmt.Errorf("subject artifact store unavailable")
		} else {
			completed.DownloadExpiresAt = completed.UpdatedAt.Add(24 * time.Hour)
			completed.ResultReference, err = s.artifacts.PutSubjectExport(ctx, workspaceID, completed.ID, raw, completed.DownloadExpiresAt)
		}
	} else {
		completed.ResultReference = "erase-evidence:" + completed.ID
		completed.BackupPending = true
	}
	if err != nil {
		completed.Status, completed.LastError = lifecyclemodel.SubjectRequestFailed, err.Error()
		return s.transitionSubject(ctx, executing, completed, principal.UserID, "lifecycle.subject.failed")
	}
	completed.Status = lifecyclemodel.SubjectRequestSucceeded
	if completed.Kind == lifecyclemodel.SubjectRequestErase {
		registration := lifecyclemodel.DeletionRegistration{RequestID: completed.ID, WorkspaceID: workspaceID, ResolvedIdentity: completed.ResolvedIdentity, BackupPending: true, Evidence: completed.ResultReference, UpdatedAt: completed.UpdatedAt}
		return s.transitionSubjectWith(ctx, executing, completed, principal.UserID, "lifecycle.subject.succeeded", func(transactionContext context.Context) error {
			return s.subjectRequests.SaveDeletionRegistration(transactionContext, registration)
		})
	}
	return s.transitionSubject(ctx, executing, completed, principal.UserID, "lifecycle.subject.succeeded")
}
