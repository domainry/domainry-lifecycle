package module

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

type preparedErasureOwner struct {
	integrationOwner
	owner                   string
	plans, executions       int
	failPlan, failExecution bool
	beforeExecution         func()
}

func (h *preparedErasureOwner) Owner(context.Context) string { return h.owner }
func (h *preparedErasureOwner) PrepareSubjectErasure(context.Context, string, string, string) (json.RawMessage, error) {
	h.plans++
	if h.failPlan {
		h.failPlan = false
		return nil, errors.New("plan unavailable")
	}
	return json.RawMessage(`{"file_reference":"/uploads/original.txt"}`), nil
}
func (h *preparedErasureOwner) ErasePreparedSubject(_ context.Context, _, _, _ string, plan json.RawMessage, _ []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	h.beforeExecution()
	h.executions++
	if string(plan) != `{"file_reference":"/uploads/original.txt"}` {
		return nil, errors.New("original cleanup pointer was lost")
	}
	if h.failExecution {
		h.failExecution = false
		return nil, errors.New("external file cleanup unavailable after record cleanup")
	}
	return json.RawMessage(`{"erased":true}`), nil
}
func (*preparedErasureOwner) EraseSubjectForRequest(context.Context, string, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return nil, errors.New("prepared owner must receive its frozen plan")
}

func TestSubjectErasurePersistsAllPlansBeforeEffectsAndReusesThemAfterFailure(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "erasure-plans"}, host)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := &preparedErasureOwner{owner: "first", failExecution: true}
	second := &preparedErasureOwner{owner: "second", failPlan: true}
	if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{Executors: []lifecyclecontract.OwnerLifecycleExecutor{integrationOwner{}}, SubjectResolver: integrationOwner{}, SubjectHandlers: []lifecyclecontract.SubjectExecutionHandler{first, second}, Artifacts: artifacts}); err != nil {
		t.Fatal(err)
	}
	requester := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "requester", Permissions: map[string]struct{}{lifecyclesdk.ActionLifecycleSubjectRequestsCreate: {}, lifecyclesdk.ActionLifecycleSubjectRequestsVerify: {}, lifecyclesdk.ActionLifecycleSubjectRequestsPreview: {}}}
	approver := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "approver", Permissions: map[string]struct{}{lifecyclesdk.ActionLifecycleSubjectRequestsApprove: {}, lifecyclesdk.ActionLifecycleSubjectRequestsExecute: {}}}
	rctx := integrationIdentityContext(t.Context(), requester, identitysdk.DataScopeOwner)
	actx := integrationIdentityContext(t.Context(), approver, identitysdk.DataScopeAll)
	g := binding.Governance()
	request, err := g.CreateSubjectRequest(rctx, lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, SubjectType: "user", SubjectID: "subject-1", Reason: "erase personal data"}, requester)
	if err != nil {
		t.Fatal(err)
	}
	if request, err = g.VerifySubjectRequest(rctx, "workspace-a", request.ID, "mfa", requester); err != nil {
		t.Fatal(err)
	}
	if request, err = g.PreviewSubjectRequest(rctx, "workspace-a", request.ID, requester); err != nil {
		t.Fatal(err)
	}
	if request, err = g.ApproveSubjectRequest(actx, "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	checkPlans := func() {
		t.Helper()
		var count int
		if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _lifecycle_subject_execution_steps WHERE workspace_id = ? AND request_id = ? AND operation = ?", "workspace-a", request.ID, "erase_plan").Scan(&count); err != nil || count != 3 {
			t.Fatalf("effects started before all plans committed: %d %v", count, err)
		}
	}
	first.beforeExecution, second.beforeExecution = checkPlans, checkPlans
	if request, err = g.ExecuteSubjectRequest(actx, "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request.Status != lifecyclemodel.SubjectRequestFailed || first.executions != 0 || second.executions != 0 {
		t.Fatalf("preparation failure caused erasure: %s %d %d", request.Status, first.executions, second.executions)
	}
	if request, err = g.ExecuteSubjectRequest(actx, "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request.Status != lifecyclemodel.SubjectRequestFailed || first.executions != 1 || second.executions != 0 {
		t.Fatalf("file failure was incorrectly completed: %s", request.Status)
	}
	if request, err = g.ExecuteSubjectRequest(actx, "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request.Status != lifecyclemodel.SubjectRequestSucceeded || first.plans != 1 || second.plans != 2 || first.executions != 2 || second.executions != 1 {
		t.Fatalf("recovery did not reuse frozen plans: status=%s plans=%d,%d executions=%d,%d", request.Status, first.plans, second.plans, first.executions, second.executions)
	}
}
