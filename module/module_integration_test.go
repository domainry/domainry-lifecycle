package module

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	internalmodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	lifecyclestore "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/lifecycle"
	migration "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/migration"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
	_ "modernc.org/sqlite"
)

type integrationHost struct {
	db        *sql.DB
	dialect   modulehost.Dialect
	registrar modulehost.MigrationRegistrar
}

func (h integrationHost) Database() modulehost.Database             { return h.db }
func (h integrationHost) Dialect() modulehost.Dialect               { return h.dialect }
func (h integrationHost) Migrations() modulehost.MigrationRegistrar { return h.registrar }
func (h integrationHost) Transactions() modulehost.Transactor       { return integrationTransactor{db: h.db} }

type integrationRegistrar struct{ runner *ormmigration.Runner }

func (r integrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, values []modulehost.SchemaMigration) error {
	if owner != migration.Owner {
		return errors.New("unexpected migration owner")
	}
	return r.runner.Apply(ctx, values)
}

type integrationTransactor struct{ db *sql.DB }

func (t integrationTransactor) WithinTransaction(ctx context.Context, operation func(context.Context, modulehost.DBTX) error) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := operation(modulehost.WithExecutor(ctx, tx), tx); err != nil {
		return err
	}
	return tx.Commit()
}

type integrationOwner struct{}

func (integrationOwner) Owner(context.Context) string { return "record" }
func (integrationOwner) Preview(context.Context, string, lifecyclemodel.PolicyVersion, time.Time) (lifecyclecontract.CleanupPreview, error) {
	return lifecyclecontract.CleanupPreview{}, nil
}
func (integrationOwner) ProcessBatch(context.Context, lifecyclemodel.CleanupJob, lifecyclemodel.PolicyVersion, []lifecyclemodel.LegalHold, int) (lifecyclemodel.CleanupBatchResult, error) {
	return lifecyclemodel.CleanupBatchResult{Done: true}, nil
}
func (integrationOwner) ResolveSubject(context.Context, string, string, string) (string, error) {
	return "subject-1", nil
}
func (integrationOwner) PreviewSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (integrationOwner) ExportSubjectForRequest(context.Context, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (integrationOwner) EraseSubjectForRequest(context.Context, string, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

type recoverableSubjectHandler struct {
	owner          string
	exportAttempts int
	failFirst      bool
}

type countingEraseHandler struct{ calls int }

func (*countingEraseHandler) Owner(context.Context) string { return "record" }
func (*countingEraseHandler) PreviewSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (*countingEraseHandler) ExportSubjectForRequest(context.Context, string, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (handler *countingEraseHandler) EraseSubjectForRequest(context.Context, string, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	handler.calls++
	return json.RawMessage(`{}`), nil
}

func (h *recoverableSubjectHandler) Owner(context.Context) string { return h.owner }
func (h *recoverableSubjectHandler) PreviewSubject(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"eligible":true}`), nil
}
func (h *recoverableSubjectHandler) ExportSubjectForRequest(_ context.Context, requestID, _, _ string) (json.RawMessage, error) {
	h.exportAttempts++
	if h.failFirst && h.exportAttempts == 1 {
		return nil, errors.New("temporary owner failure")
	}
	return json.Marshal(map[string]string{"owner": h.owner, "request_id": requestID})
}
func (*recoverableSubjectHandler) EraseSubjectForRequest(context.Context, string, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return json.RawMessage(`{"erased":true}`), nil
}

func newIntegrationHost(t *testing.T) integrationHost {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	renderer := dialect.WithSchema("")
	runner, err := ormmigration.NewRunner(db, renderer, ormmigration.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return integrationHost{db: db, dialect: renderer, registrar: integrationRegistrar{runner: runner}}
}

func integrationIdentityContext(ctx context.Context, principal lifecycleaccess.Principal, scope identitysdk.DataScope) context.Context {
	bundle := &identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(principal.WorkspaceID), SubjectID: identitysdk.SubjectID(principal.UserID), OrgID: "org-a", OrgScopeIDs: []string{"org-a"}}}
	for permission := range principal.Permissions {
		separator := strings.LastIndex(permission, ".")
		resource, action := permission[:separator], permission[separator+1:]
		bundle.FunctionGrants = append(bundle.FunctionGrants, identitysdk.FunctionGrant{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
		bundle.DataPolicies = append(bundle.DataPolicies, identitysdk.DataPolicy{Key: "integration-" + permission, Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow, DataScopes: []identitysdk.DataScope{scope}})
	}
	return identitysdk.WithRequestIdentity(ctx, identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, AccessBundle: bundle}})
}

func TestBindingOwnsApplicationPersistenceAndHostTransaction(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "integration-test"}, host)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := integrationOwner{}
	if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{Executors: []lifecyclecontract.OwnerLifecycleExecutor{owner}, SubjectResolver: owner, SubjectHandlers: []lifecyclecontract.SubjectExecutionHandler{owner}, Artifacts: artifacts}); err != nil {
		t.Fatal(err)
	}
	if binding.Governance() == nil || binding.System() == nil {
		t.Fatal("Lifecycle business capabilities were not exposed after owner binding")
	}
	provider, ok := binding.(modulehttp.Provider)
	if !ok || len(provider.HTTPSurfaces()) != 1 || len(provider.HTTPSurfaces()[0].Routes()) != 17 {
		t.Fatalf("Lifecycle HTTP surfaces=%#v", provider)
	}
	if err := modulehttp.ValidateSurface(provider.HTTPSurfaces()[0]); err != nil {
		t.Fatal(err)
	}
	actionProvider, ok := binding.(actioncontract.Provider)
	if !ok {
		t.Fatal("Lifecycle binding does not expose its complete Action manifest")
	}
	actions, err := actionProvider.AuthorizationActions()
	if err != nil || len(actions) != 21 {
		t.Fatalf("Lifecycle Actions=%d err=%v", len(actions), err)
	}
	principal := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin", Permissions: map[string]struct{}{
		lifecyclesdk.ActionLifecyclePoliciesPublish: {},
		lifecyclesdk.ActionLifecyclePoliciesList:    {},
	}}
	ctx := integrationIdentityContext(t.Context(), principal, identitysdk.DataScopeAll)
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "records.v1", Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct, DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, WorkspaceMayExtend: true, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}, Revision: 1}
	created, err := binding.Governance().PublishPolicy(ctx, policy, principal)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID != "workspace-a" || created.Status != lifecyclemodel.PolicyStatusPublished || created.PublishedAt.IsZero() {
		t.Fatalf("server-owned policy fields were not populated: %#v", created)
	}
	policies, err := binding.Governance().ListPolicies(ctx, principal)
	if err != nil || len(policies) != 1 {
		t.Fatalf("policies=%#v err=%v", policies, err)
	}
	var auditRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _lifecycle_audit_evidence WHERE workspace_id = ?", "workspace-a").Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("audit rows=%d err=%v", auditRows, err)
	}
	var migrationRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _schema_migrations").Scan(&migrationRows); err != nil || migrationRows != 3 {
		t.Fatalf("migration ledger rows=%d err=%v", migrationRows, err)
	}
	if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{}); err == nil {
		t.Fatal("Lifecycle owner extensions were rebound")
	}
}

func TestSubjectExecutionRetryReusesCompletedOwnerSteps(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "subject-recovery-test"}, host)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resolver := integrationOwner{}
	first := &recoverableSubjectHandler{owner: "first"}
	second := &recoverableSubjectHandler{owner: "second", failFirst: true}
	if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{
		Executors:       []lifecyclecontract.OwnerLifecycleExecutor{resolver},
		SubjectResolver: resolver,
		SubjectHandlers: []lifecyclecontract.SubjectExecutionHandler{first, second},
		Artifacts:       artifacts,
	}); err != nil {
		t.Fatal(err)
	}
	requester := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "requester", Permissions: map[string]struct{}{
		lifecyclesdk.ActionLifecycleSubjectRequestsCreate:  {},
		lifecyclesdk.ActionLifecycleSubjectRequestsVerify:  {},
		lifecyclesdk.ActionLifecycleSubjectRequestsPreview: {},
	}}
	approver := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "approver", Permissions: map[string]struct{}{
		lifecyclesdk.ActionLifecycleSubjectRequestsApprove: {},
		lifecyclesdk.ActionLifecycleSubjectRequestsExecute: {},
	}}
	requesterContext := integrationIdentityContext(t.Context(), requester, identitysdk.DataScopeOwner)
	approverContext := integrationIdentityContext(t.Context(), approver, identitysdk.DataScopeAll)
	governance := binding.Governance()
	request, err := governance.CreateSubjectRequest(requesterContext, lifecyclemodel.SubjectRequest{
		WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport,
		SubjectType: "user", SubjectID: "subject-1", Reason: "portability request",
	}, requester)
	if err != nil {
		t.Fatal(err)
	}
	if request, err = governance.VerifySubjectRequest(requesterContext, "workspace-a", request.ID, "mfa-1", requester); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.PreviewSubjectRequest(requesterContext, "workspace-a", request.ID, requester); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.ApproveSubjectRequest(approverContext, "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.ExecuteSubjectRequest(approverContext, "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request.Status != lifecyclemodel.SubjectRequestFailed || first.exportAttempts != 1 || second.exportAttempts != 1 {
		t.Fatalf("first execution request=%#v attempts=(%d,%d)", request, first.exportAttempts, second.exportAttempts)
	}
	if request, err = governance.ExecuteSubjectRequest(approverContext, "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request.Status != lifecyclemodel.SubjectRequestSucceeded || first.exportAttempts != 1 || second.exportAttempts != 2 {
		t.Fatalf("retried execution request=%#v attempts=(%d,%d)", request, first.exportAttempts, second.exportAttempts)
	}
	var stepRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _lifecycle_subject_execution_steps WHERE workspace_id = ? AND request_id = ?", "workspace-a", request.ID).Scan(&stepRows); err != nil || stepRows != 2 {
		t.Fatalf("execution step rows=%d err=%v", stepRows, err)
	}
}

func TestDeletionReplayPreflightsWholeBatchBeforeOwnerSideEffects(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "deletion-replay-test"}, host)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := integrationOwner{}
	handler := &countingEraseHandler{}
	if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{
		Executors: []lifecyclecontract.OwnerLifecycleExecutor{owner}, SubjectResolver: owner,
		SubjectHandlers: []lifecyclecontract.SubjectExecutionHandler{handler}, Artifacts: artifacts,
	}); err != nil {
		t.Fatal(err)
	}
	store := lifecyclestore.NewLifecycleStore(host)
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	for _, request := range []internalmodel.SubjectRequest{
		{ID: "request-a", WorkspaceID: "workspace-a", Kind: internalmodel.SubjectRequestErase, Status: internalmodel.SubjectRequestSucceeded, SubjectID: "subject-a", ResolvedIdentity: "identity-a", RequestedBy: "requester", CreatedAt: now, UpdatedAt: now},
		{ID: "request-b", WorkspaceID: "workspace-a", Kind: internalmodel.SubjectRequestErase, Status: internalmodel.SubjectRequestSucceeded, SubjectID: "subject-b", ResolvedIdentity: "identity-b", RequestedBy: "requester", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveSubjectRequest(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveDeletionRegistration(t.Context(), internalmodel.DeletionRegistration{RequestID: request.ID, WorkspaceID: request.WorkspaceID, ResolvedIdentity: request.ResolvedIdentity, BackupPending: true, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveLegalHold(t.Context(), internalmodel.LegalHold{
		ID: "hold-b", WorkspaceID: "workspace-a", ResourceType: "data_subject", ResourceID: "identity-b",
		CreatedBy: "legal", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	principal := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator", Permissions: map[string]struct{}{lifecyclesdk.ActionLifecycleDeletionsReplay: {}}}
	ctx := integrationIdentityContext(t.Context(), principal, identitysdk.DataScopeAll)
	if replayed, err := binding.Governance().ReplayRegisteredDeletions(ctx, "workspace-a", 10, principal); err == nil || replayed != 0 {
		t.Fatalf("replayed=%d err=%v", replayed, err)
	}
	if handler.calls != 0 {
		t.Fatalf("owner side effects started before full-batch preflight: calls=%d", handler.calls)
	}
	var audits int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _lifecycle_audit_evidence WHERE workspace_id = ? AND event = ?", "workspace-a", "lifecycle.deletion.replayed").Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("replay audit rows=%d err=%v", audits, err)
	}
}
