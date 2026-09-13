package module

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	sdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/access"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	model "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
)

func TestAccountErasureQueueCommitsWithActionAndExecutesAfterCommit(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), sdk.ApplicationRef{RuntimeID: "action-erasure"}, host)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := &countingEraseHandler{}
	if err = binding.BindOwners(t.Context(), sdk.OwnerExtensions{Executors: []contract.OwnerLifecycleExecutor{integrationOwner{}}, SubjectResolver: integrationOwner{}, SubjectHandlers: []contract.SubjectExecutionHandler{owner}, Artifacts: artifacts}); err != nil {
		t.Fatal(err)
	}
	queue := binding.(sdk.AccountErasureBinding).AccountErasures()
	if queue == nil {
		t.Fatal("account erasure port not bound")
	}
	scope := access.NewSystemScope(access.SystemScopeGlobal, "project action account erasure")
	ctx := requestcontext.WithWorkspaceID(t.Context(), "workspace-a")
	approval := contract.AccountErasureApproval{WorkspaceID: "workspace-a", RequestID: "action-erasure-1", SubjectID: "subject-1", RequestedBy: "subject-1", ApprovedBy: "operator",
		OwnerOrgID: "org-a", ActionKey: "member.process_deletion", ApprovalID: "governance-request-1", BindingKey: "member", ObjectKey: "member", ProfileID: "member-1"}
	reference := contract.AccountErasureReference{WorkspaceID: approval.WorkspaceID, RequestID: approval.RequestID, OwnerOrgID: approval.OwnerOrgID, BindingKey: approval.BindingKey, ObjectKey: approval.ObjectKey, ProfileID: approval.ProfileID}
	if _, err = queue.StageApprovedAccountErasure(ctx, approval, scope); err == nil {
		t.Fatal("queue accepted without Action transaction")
	}
	abort := errors.New("Action failed after staging")
	err = host.Transactions().WithinTransaction(ctx, func(txctx context.Context, _ modulehost.DBTX) error {
		queued, err := queue.StageApprovedAccountErasure(txctx, approval, scope)
		if err != nil {
			return err
		}
		if queued.Status != model.SubjectRequestApproved || owner.calls != 0 {
			t.Fatalf("staging ran erasure: status=%s calls=%d", queued.Status, owner.calls)
		}
		if _, err = queue.ProcessApprovedAccountErasures(txctx, "worker", 10, time.Now().UTC(), scope); err == nil {
			t.Fatal("file/owner effects permitted inside Action transaction")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	for _, table := range []string{"_lifecycle_subject_requests", "_lifecycle_account_erasure_approvals", "_lifecycle_audit_evidence"} {
		var count int
		if err = host.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("Action rollback left %s rows=%d err=%v", table, count, err)
		}
	}
	stage := func(command contract.AccountErasureApproval) error {
		return host.Transactions().WithinTransaction(ctx, func(txctx context.Context, _ modulehost.DBTX) error {
			_, err := queue.StageApprovedAccountErasure(txctx, command, scope)
			return err
		})
	}
	selfApproved := approval
	selfApproved.ApprovedBy = selfApproved.RequestedBy
	if err = stage(selfApproved); err == nil {
		t.Fatal("independent approval bypassed")
	}
	otherRequester := approval
	otherRequester.RequestedBy = "other-subject"
	if err = stage(otherRequester); err == nil {
		t.Fatal("request accepted for another subject")
	}
	if err = stage(approval); err != nil {
		t.Fatal(err)
	}
	if err = stage(approval); err != nil {
		t.Fatalf("Action retry not idempotent: %v", err)
	}
	conflict := approval
	conflict.ProfileID = "other-member"
	if err = stage(conflict); err == nil {
		t.Fatal("request ID reused for another profile")
	}
	wrongOrg := reference
	wrongOrg.OwnerOrgID = "other-org"
	if _, err = queue.GetAccountErasure(ctx, wrongOrg, scope); err == nil {
		t.Fatal("cross-organization receipt read accepted")
	}
	wrongWorkspace := reference
	wrongWorkspace.WorkspaceID = "other-workspace"
	if _, err = queue.GetAccountErasure(ctx, wrongWorkspace, scope); err == nil {
		t.Fatal("cross-workspace receipt read accepted")
	}
	wrongProfile := reference
	wrongProfile.ProfileID = "other-member"
	if _, err = queue.GetAccountErasure(ctx, wrongProfile, scope); err == nil {
		t.Fatal("cross-profile receipt read accepted")
	}
	wrongBinding := reference
	wrongBinding.BindingKey = "employee"
	wrongBinding.ObjectKey = "employee"
	if _, err = queue.GetAccountErasure(ctx, wrongBinding, scope); err == nil {
		t.Fatal("undeclared profile binding receipt read accepted")
	}
	if queued, err := queue.GetAccountErasure(ctx, reference, scope); err != nil || queued.Status != model.SubjectRequestApproved || owner.calls != 0 {
		t.Fatalf("queued=%+v calls=%d err=%v", queued, owner.calls, err)
	}
	if processed, err := queue.ProcessApprovedAccountErasures(ctx, "worker", 10, time.Now().UTC(), scope); err != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
	completed, err := queue.GetAccountErasure(ctx, reference, scope)
	if err != nil || completed.Status != model.SubjectRequestSucceeded || owner.calls != 1 {
		t.Fatalf("completed=%+v calls=%d err=%v", completed, owner.calls, err)
	}
	if processed, err := queue.ProcessApprovedAccountErasures(ctx, "worker", 10, time.Now().UTC(), scope); err != nil || processed != 0 || owner.calls != 1 {
		t.Fatalf("completed request reran: processed=%d calls=%d err=%v", processed, owner.calls, err)
	}
	if err = stage(approval); err != nil {
		t.Fatalf("completed Action replay failed: %v", err)
	}
}

func TestAccountErasureQueueRetriesFailedCleanupWithDurablePlan(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), sdk.ApplicationRef{RuntimeID: "action-erasure-retry"}, host)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := &preparedErasureOwner{owner: "record", failExecution: true, beforeExecution: func() {}}
	if err = binding.BindOwners(t.Context(), sdk.OwnerExtensions{Executors: []contract.OwnerLifecycleExecutor{integrationOwner{}}, SubjectResolver: integrationOwner{}, SubjectHandlers: []contract.SubjectExecutionHandler{owner}, Artifacts: artifacts}); err != nil {
		t.Fatal(err)
	}
	queue := binding.(sdk.AccountErasureBinding).AccountErasures()
	scope := access.NewSystemScope(access.SystemScopeGlobal, "project action account erasure")
	ctx := requestcontext.WithWorkspaceID(t.Context(), "workspace-a")
	approval := contract.AccountErasureApproval{WorkspaceID: "workspace-a", RequestID: "action-erasure-retry", SubjectID: "subject-1", RequestedBy: "subject-1", ApprovedBy: "operator",
		OwnerOrgID: "org-a", ActionKey: "member.process_deletion", ApprovalID: "governance-request-2", BindingKey: "member", ObjectKey: "member", ProfileID: "member-1"}
	reference := contract.AccountErasureReference{WorkspaceID: approval.WorkspaceID, RequestID: approval.RequestID, OwnerOrgID: approval.OwnerOrgID, BindingKey: approval.BindingKey, ObjectKey: approval.ObjectKey, ProfileID: approval.ProfileID}
	if err = host.Transactions().WithinTransaction(ctx, func(txctx context.Context, _ modulehost.DBTX) error {
		_, err := queue.StageApprovedAccountErasure(txctx, approval, scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if processed, err := queue.ProcessApprovedAccountErasures(ctx, "worker", 10, time.Now().UTC(), scope); err != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
	failed, err := queue.GetAccountErasure(ctx, reference, scope)
	if err != nil || failed.Status != model.SubjectRequestFailed || failed.LastError == "" || owner.executions != 1 {
		t.Fatalf("failed cleanup reported success: %+v calls=%d err=%v", failed, owner.executions, err)
	}
	if processed, err := queue.ProcessApprovedAccountErasures(ctx, "worker", 10, time.Now().UTC(), scope); err != nil || processed != 0 {
		t.Fatalf("retry backoff ignored: processed=%d err=%v", processed, err)
	}
	if processed, err := queue.ProcessApprovedAccountErasures(ctx, "worker", 10, time.Now().UTC().Add(31*time.Second), scope); err != nil || processed != 1 {
		t.Fatalf("retry processed=%d err=%v", processed, err)
	}
	completed, err := queue.GetAccountErasure(ctx, reference, scope)
	if err != nil || completed.Status != model.SubjectRequestSucceeded || completed.ExecutionAttempt != 2 || owner.executions != 2 || owner.plans != 1 {
		t.Fatalf("durable retry lost original plan: %+v plans=%d calls=%d err=%v", completed, owner.plans, owner.executions, err)
	}
}
