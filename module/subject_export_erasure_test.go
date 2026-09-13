package module

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	sdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/access"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	model "github.com/domainry/domainry-lifecycle-sdk/model"
)

type personalExportOwner struct{ integrationOwner }

func (personalExportOwner) ResolveSubject(_ context.Context, _, _, subjectID string) (string,error) { return subjectID,nil }

func (personalExportOwner) ExportSubjectForRequest(context.Context, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"email":"PRIVATE@example.test"}`), nil
}

type failingExportDeleter struct {
	contract.SubjectArtifactStore
	fail bool
}

func (s *failingExportDeleter) DeleteSubjectExport(ctx context.Context, workspaceID, reference string) error {
	if s.fail {
		s.fail = false
		return errors.New("temporary subject export delete failure")
	}
	return s.SubjectArtifactStore.(contract.SubjectExportDeleter).DeleteSubjectExport(ctx, workspaceID, reference)
}

func TestErasureRemovesPriorExportsAndSnapshotsAndBlocksNewExports(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), sdk.ApplicationRef{RuntimeID: "subject-export-erasure"}, host)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := &failingExportDeleter{SubjectArtifactStore: artifacts}
	owner := personalExportOwner{}
	if err = binding.BindOwners(t.Context(), sdk.OwnerExtensions{Executors: []contract.OwnerLifecycleExecutor{owner}, SubjectResolver: owner, SubjectHandlers: []contract.SubjectExecutionHandler{owner}, Artifacts: files}); err != nil {
		t.Fatal(err)
	}
	permissions := map[string]struct{}{}
	for _, permission := range []string{sdk.ActionLifecycleSubjectRequestsCreate, sdk.ActionLifecycleSubjectRequestsVerify, sdk.ActionLifecycleSubjectRequestsPreview, sdk.ActionLifecycleSubjectRequestsApprove, sdk.ActionLifecycleSubjectRequestsExecute, sdk.ActionLifecycleSubjectRequestsRead, sdk.ActionLifecycleSubjectExportsDownload} {
		permissions[permission] = struct{}{}
	}
	requester := access.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "requester", Permissions: permissions}
	operator := requester
	operator.UserID = "operator"
	rctx := integrationIdentityContext(t.Context(), requester, identitysdk.DataScopeAll)
	actx := integrationIdentityContext(t.Context(), operator, identitysdk.DataScopeAll)
	g := binding.Governance()
	approve := func(kind model.SubjectRequestKind, subject string) model.SubjectRequest {
		t.Helper()
		r, err := g.CreateSubjectRequest(rctx, model.SubjectRequest{WorkspaceID: "workspace-a", Kind: kind, SubjectType: "user", SubjectID: subject, Reason: "PRIVATE reason"}, requester)
		if err != nil {
			t.Fatal(err)
		}
		if r, err = g.VerifySubjectRequest(rctx, "workspace-a", r.ID, "PRIVATE verification", requester); err != nil {
			t.Fatal(err)
		}
		if r, err = g.PreviewSubjectRequest(rctx, "workspace-a", r.ID, requester); err != nil {
			t.Fatal(err)
		}
		if r, err = g.ApproveSubjectRequest(actx, "workspace-a", r.ID, operator); err != nil {
			t.Fatal(err)
		}
		return r
	}
	export := approve(model.SubjectRequestExport, "subject-one")
	if export, err = g.ExecuteSubjectRequest(actx, "workspace-a", export.ID, operator); err != nil || export.Status != model.SubjectRequestSucceeded {
		t.Fatalf("export failed: %+v %v", export, err)
	}
	other := approve(model.SubjectRequestExport, "subject-other")
	if other, err = g.ExecuteSubjectRequest(actx, "workspace-a", other.ID, operator); err != nil || other.Status != model.SubjectRequestSucceeded {
		t.Fatalf("other export failed: %+v %v", other, err)
	}
	before, err := g.DownloadSubjectExport(actx, "workspace-a", export.ID, operator, time.Now().UTC())
	if err != nil || !strings.Contains(string(before), "PRIVATE") {
		t.Fatalf("missing personal fixture: %s %v", before, err)
	}
	erase := approve(model.SubjectRequestErase, "subject-one")
	files.fail = true
	if erase, err = g.ExecuteSubjectRequest(actx, "workspace-a", erase.ID, operator); err != nil || erase.Status != model.SubjectRequestFailed {
		t.Fatalf("delete failure falsely succeeded: %+v %v", erase, err)
	}
	if erase, err = g.ExecuteSubjectRequest(actx, "workspace-a", erase.ID, operator); err != nil || erase.Status != model.SubjectRequestSucceeded {
		t.Fatalf("erasure retry failed: %+v %v", erase, err)
	}
	if _, err = g.DownloadSubjectExport(actx, "workspace-a", export.ID, operator, time.Now().UTC()); err == nil {
		t.Fatal("prior export still downloadable")
	}
	if err = artifacts.(contract.SubjectExportDeleter).DeleteSubjectExport(t.Context(), "workspace-other", other.ResultReference); err == nil {
		t.Fatal("cross workspace artifact deletion accepted")
	}
	if _, err = g.DownloadSubjectExport(actx, "workspace-a", other.ID, operator, time.Now().UTC()); err != nil {
		t.Fatalf("other subject export removed: %v", err)
	}
	var snapshots int
	if err = host.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _lifecycle_subject_execution_steps WHERE workspace_id=? AND request_id=?`, "workspace-a", export.ID).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatalf("personal execution copies remain: %d %v", snapshots, err)
	}
	var raw string
	if err = host.db.QueryRowContext(t.Context(), `SELECT payload_json FROM _lifecycle_subject_requests WHERE workspace_id=? AND id=?`, "workspace-a", export.ID).Scan(&raw); err != nil || strings.Contains(raw, "PRIVATE") {
		t.Fatalf("request personal data remains: %s %v", raw, err)
	}
	newExport, err := g.CreateSubjectRequest(rctx, model.SubjectRequest{WorkspaceID: "workspace-a", Kind: model.SubjectRequestExport, SubjectType: "user", SubjectID: "subject-one", Reason: "new export"}, requester)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.VerifySubjectRequest(rctx, "workspace-a", newExport.ID, "mfa", requester); err == nil {
		t.Fatal("erased subject obtained a new export")
	}
}
