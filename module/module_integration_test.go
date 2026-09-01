package module

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
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
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "records.v1", Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct, DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, WorkspaceMayExtend: true, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}, Revision: 1}
	created, err := binding.Governance().PublishPolicy(t.Context(), policy, principal)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID != "workspace-a" || created.Status != lifecyclemodel.PolicyStatusPublished || created.PublishedAt.IsZero() {
		t.Fatalf("server-owned policy fields were not populated: %#v", created)
	}
	policies, err := binding.Governance().ListPolicies(t.Context(), principal)
	if err != nil || len(policies) != 1 {
		t.Fatalf("policies=%#v err=%v", policies, err)
	}
	var auditRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _lifecycle_audit_evidence WHERE workspace_id = ?", "workspace-a").Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("audit rows=%d err=%v", auditRows, err)
	}
	var migrationRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _schema_migrations").Scan(&migrationRows); err != nil || migrationRows != 2 {
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
	governance := binding.Governance()
	request, err := governance.CreateSubjectRequest(t.Context(), lifecyclemodel.SubjectRequest{
		WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport,
		SubjectType: "user", SubjectID: "subject-1", Reason: "portability request",
	}, requester)
	if err != nil {
		t.Fatal(err)
	}
	if request, err = governance.VerifySubjectRequest(t.Context(), "workspace-a", request.ID, "mfa-1", requester); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.PreviewSubjectRequest(t.Context(), "workspace-a", request.ID, requester); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.ApproveSubjectRequest(t.Context(), "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request, err = governance.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, approver); err != nil {
		t.Fatal(err)
	}
	if request.Status != lifecyclemodel.SubjectRequestFailed || first.exportAttempts != 1 || second.exportAttempts != 1 {
		t.Fatalf("first execution request=%#v attempts=(%d,%d)", request, first.exportAttempts, second.exportAttempts)
	}
	if request, err = governance.ExecuteSubjectRequest(t.Context(), "workspace-a", request.ID, approver); err != nil {
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
