package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	sdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/access"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	model "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	repository "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
)

func (s *LifecycleApplicationService) accountErasureRepository() (repository.AccountErasureRepository, error) {
	repo, ok := s.subjectRequests.(repository.AccountErasureRepository)
	if !ok {
		return nil, fmt.Errorf("account erasure queue unavailable")
	}
	return repo, nil
}

func validateAccountErasureScope(ctx context.Context, workspace string, scope access.SystemScope) error {
	if scope.Kind != access.SystemScopeGlobal || !scope.Valid() {
		return fmt.Errorf("account erasure requires trusted project Action scope")
	}
	if strings.TrimSpace(workspace) == "" || workspace != requestcontext.WorkspaceID(ctx) {
		return fmt.Errorf("account erasure workspace mismatch")
	}
	return nil
}

func (s *LifecycleApplicationService) StageApprovedAccountErasure(ctx context.Context, approval contract.AccountErasureApproval, scope access.SystemScope) (model.SubjectRequest, error) {
	if err := validateAccountErasureScope(ctx, approval.WorkspaceID, scope); err != nil {
		return model.SubjectRequest{}, err
	}
	if modulehost.ExecutorFromContext(ctx, nil) == nil {
		return model.SubjectRequest{}, fmt.Errorf("account erasure requires host Action transaction")
	}
	for _, value := range []string{approval.RequestID, approval.SubjectID, approval.RequestedBy, approval.ApprovedBy, approval.OwnerOrgID, approval.ActionKey, approval.ApprovalID, approval.BindingKey, approval.ObjectKey, approval.ProfileID} {
		if strings.TrimSpace(value) == "" {
			return model.SubjectRequest{}, fmt.Errorf("account erasure approval incomplete")
		}
	}
	if approval.RequestedBy != approval.SubjectID || approval.ApprovedBy == approval.RequestedBy {
		return model.SubjectRequest{}, fmt.Errorf("account erasure requires the subject's request and independent business approval")
	}
	repo, err := s.accountErasureRepository()
	if err != nil {
		return model.SubjectRequest{}, err
	}
	if saved, found, err := repo.GetAccountErasureApproval(ctx, approval.WorkspaceID, approval.RequestID); err != nil {
		return model.SubjectRequest{}, err
	} else if found {
		if saved != approval {
			return model.SubjectRequest{}, fmt.Errorf("account erasure request reused for another approval")
		}
		return s.subjectRequest(ctx, approval.WorkspaceID, approval.RequestID, repository.UnrestrictedDataScopeFilter())
	}
	holds, err := s.legalHolds.ActiveLegalHolds(ctx, model.ResourceTarget{WorkspaceID: approval.WorkspaceID, ResourceType: "data_subject", ResourceID: approval.SubjectID}, time.Now().UTC())
	if err != nil {
		return model.SubjectRequest{}, err
	}
	if len(holds) > 0 {
		return model.SubjectRequest{}, fmt.Errorf("account erasure blocked by legal hold")
	}
	now := time.Now().UTC()
	request := model.SubjectRequest{ID: approval.RequestID, WorkspaceID: approval.WorkspaceID, Kind: model.SubjectRequestErase, Status: model.SubjectRequestApproved,
		SubjectType: "user", SubjectID: approval.SubjectID, ResolvedIdentity: approval.SubjectID, RequestedBy: approval.RequestedBy, ApprovedBy: approval.ApprovedBy,
		OwnerOrgID: approval.OwnerOrgID, Reason: "approved project Action account erasure", CreatedAt: now, UpdatedAt: now}
	if err = s.subjectRequests.SaveSubjectRequest(ctx, request); err != nil {
		return model.SubjectRequest{}, err
	}
	if err = repo.SaveAccountErasureApproval(ctx, approval); err != nil {
		return model.SubjectRequest{}, err
	}
	if err = s.audit(ctx, approval.WorkspaceID, "lifecycle.account_erasure.staged", approval.ApprovedBy, approval.RequestID, "", approval); err != nil {
		return model.SubjectRequest{}, err
	}
	return request, nil
}

func (s *LifecycleApplicationService) GetAccountErasure(ctx context.Context, reference contract.AccountErasureReference, scope access.SystemScope) (model.SubjectRequest, error) {
	if err := validateAccountErasureScope(ctx, reference.WorkspaceID, scope); err != nil {
		return model.SubjectRequest{}, err
	}
	repo, err := s.accountErasureRepository()
	if err != nil {
		return model.SubjectRequest{}, err
	}
	approval, found, err := repo.GetAccountErasureApproval(ctx, reference.WorkspaceID, reference.RequestID)
	if err != nil {
		return model.SubjectRequest{}, err
	}
	if !found || strings.TrimSpace(reference.OwnerOrgID) == "" || approval.OwnerOrgID != reference.OwnerOrgID ||
		approval.BindingKey != reference.BindingKey || approval.ObjectKey != reference.ObjectKey || approval.ProfileID != reference.ProfileID {
		return model.SubjectRequest{}, fmt.Errorf("account erasure approval not in authorized profile and organization")
	}
	return s.subjectRequest(ctx, reference.WorkspaceID, reference.RequestID, repository.UnrestrictedDataScopeFilter())
}

func (s *LifecycleApplicationService) ProcessApprovedAccountErasures(ctx context.Context, leaseOwner string, limit int, now time.Time, scope access.SystemScope) (int, error) {
	if !scope.Valid() || scope.Kind != access.SystemScopeGlobal || strings.TrimSpace(leaseOwner) == "" {
		return 0, fmt.Errorf("account erasure worker scope invalid")
	}
	if modulehost.ExecutorFromContext(ctx, nil) != nil {
		return 0, fmt.Errorf("account erasure effects cannot run inside an Action transaction")
	}
	repo, err := s.accountErasureRepository()
	if err != nil {
		return 0, err
	}
	requests, err := repo.ListRunnableAccountErasures(ctx, limit, now, scope)
	if err != nil {
		return 0, err
	}
	processed := 0
	principal := access.NewSystemPrincipal(leaseOwner, scope, sdk.ActionLifecycleSubjectRequestsExecute)
	for _, request := range requests {
		requestCtx := requestcontext.WithWorkspaceID(ctx, request.WorkspaceID)
		if _, err = s.ExecuteSubjectRequest(requestCtx, request.WorkspaceID, request.ID, principal); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}
