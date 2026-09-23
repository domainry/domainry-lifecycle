package module

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	actioncontract "github.com/domainry/domainry-foundation/action"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
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
	artifactfixture "github.com/domainry/domainry-lifecycle/internal/testsupport/artifactfixture"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
	_ "modernc.org/sqlite"
)

type integrationHost struct {
	db           *sql.DB
	dialect      modulehost.Dialect
	registrar    *integrationRegistrar
	transactions modulehost.Transactor
	definitions  metadatasdk.DefinitionStore
	audit        *integrationAudit
	artifacts    *artifactfixture.Store
	content      *artifactfixture.Content
}

func (h integrationHost) Database() modulehost.Database             { return h.db }
func (h integrationHost) Dialect() modulehost.Dialect               { return h.dialect }
func (h integrationHost) Migrations() modulehost.MigrationRegistrar { return h.registrar }
func (h integrationHost) Transactions() modulehost.Transactor       { return h.transactions }
func (h integrationHost) DefinitionStore() metadatasdk.DefinitionStore {
	return h.definitions
}
func (h integrationHost) AuditAppender() auditcontract.Appender {
	if h.audit == nil {
		return nil
	}
	return h.audit
}
func (h integrationHost) AuditTransactionalAppender() auditcontract.TransactionalAppender {
	if h.audit == nil {
		return nil
	}
	return h.audit
}
func (h integrationHost) ArtifactStore() sharedartifact.ManagedStore { return h.artifacts }
func (h integrationHost) ArtifactContentStore() lifecyclecontract.ArtifactContentStore {
	return h.content
}
func (h integrationHost) ArtifactContentWriter() lifecyclecontract.ArtifactContentWriter {
	return h.content
}

type integrationAudit struct{ db *sql.DB }

func (a *integrationAudit) Append(ctx context.Context, request auditcontract.AppendRequest) (auditcontract.Event, error) {
	event, err := auditcontract.BuildEvent(request, time.Now().UTC())
	if err != nil {
		return event, err
	}
	metadata, _ := json.Marshal(event.Metadata)
	after, _ := json.Marshal(event.After)
	_, err = a.db.ExecContext(ctx, `INSERT INTO _audit_events (workspace_id,id,family,event,object_key,record_id,actor_id,summary,metadata_json,after_json,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, event.WorkspaceID, event.ID, event.Family, event.Event, event.ObjectKey, event.RecordID, event.ActorID, event.Summary, string(metadata), string(after), event.CreatedAt)
	return event, err
}

func (*integrationAudit) AppendWithin(ctx context.Context, transaction auditcontract.Transaction, request auditcontract.AppendRequest) (auditcontract.Event, error) {
	event, err := auditcontract.BuildEvent(request, time.Now().UTC())
	if err != nil {
		return event, err
	}
	metadata, _ := json.Marshal(event.Metadata)
	after, _ := json.Marshal(event.After)
	_, err = transaction.ExecContext(ctx, `INSERT INTO _audit_events (workspace_id,id,family,event,object_key,record_id,actor_id,summary,metadata_json,after_json,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, event.WorkspaceID, event.ID, event.Family, event.Event, event.ObjectKey, event.RecordID, event.ActorID, event.Summary, string(metadata), string(after), event.CreatedAt)
	return event, err
}

type integrationMigrationCall struct {
	owner      string
	migrations []modulehost.SchemaMigration
}

type integrationRegistrar struct {
	database  *sql.DB
	mu        sync.Mutex
	calls     []integrationMigrationCall
	checksums map[string]map[uint]string
}

func (r *integrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, values []modulehost.SchemaMigration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner != migration.Owner && owner != "metadata" {
		return errors.New("unexpected migration owner")
	}
	if r.checksums == nil {
		r.checksums = map[string]map[uint]string{}
	}
	if _, err := r.database.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS _schema_migrations (owner TEXT NOT NULL, version INTEGER NOT NULL, name TEXT NOT NULL, checksum TEXT NOT NULL, PRIMARY KEY (owner, version))`); err != nil {
		return err
	}
	if r.checksums[owner] == nil {
		r.checksums[owner] = map[uint]string{}
	}
	for _, value := range values {
		checksum := ormmigration.Checksum(value)
		if existing, found := r.checksums[owner][value.Version]; found {
			if existing != checksum {
				return errors.New("migration checksum drift")
			}
			continue
		}
		var persisted string
		err := r.database.QueryRowContext(ctx, `SELECT checksum FROM _schema_migrations WHERE owner=? AND version=?`, owner, value.Version).Scan(&persisted)
		if err == nil {
			if persisted != checksum {
				return errors.New("migration checksum drift")
			}
			r.checksums[owner][value.Version] = persisted
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		tx, err := r.database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, statement := range value.Statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO _schema_migrations (owner, version, name, checksum) VALUES (?, ?, ?, ?)`, owner, value.Version, value.Name, checksum); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		r.checksums[owner][value.Version] = checksum
	}
	r.calls = append(r.calls, integrationMigrationCall{owner: owner, migrations: append([]modulehost.SchemaMigration(nil), values...)})
	return nil
}

func (r *integrationRegistrar) snapshot() []integrationMigrationCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]integrationMigrationCall(nil), r.calls...)
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

type rollbackOnceIntegrationTransactor struct {
	db      *sql.DB
	mu      sync.Mutex
	pending bool
}

func (t *rollbackOnceIntegrationTransactor) WithinTransaction(ctx context.Context, operation func(context.Context, modulehost.DBTX) error) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := operation(modulehost.WithExecutor(ctx, tx), tx); err != nil {
		return err
	}
	t.mu.Lock()
	rollback := t.pending
	t.pending = false
	t.mu.Unlock()
	if rollback {
		return errors.New("integration host forced transaction rollback")
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
	previewReady   *sync.WaitGroup
	previewRelease <-chan struct{}
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
	if h.previewReady != nil {
		h.previewReady.Done()
		<-h.previewRelease
	}
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
	registrar := &integrationRegistrar{database: db}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE _audit_events (workspace_id TEXT NOT NULL, id TEXT NOT NULL, family TEXT NOT NULL, event TEXT NOT NULL, object_key TEXT NOT NULL, record_id TEXT NOT NULL, actor_id TEXT NOT NULL, summary TEXT NOT NULL, metadata_json TEXT NOT NULL, after_json TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY (workspace_id,id))`); err != nil {
		t.Fatal(err)
	}
	host := integrationHost{
		db: db, dialect: renderer, registrar: registrar, transactions: integrationTransactor{db: db},
		audit: &integrationAudit{db: db}, artifacts: artifactfixture.NewStore(), content: artifactfixture.NewContent(),
	}
	host.definitions = newIntegrationDefinitionStore(t, host)
	return host
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

func TestModuleRequiresSharedAuditAppender(t *testing.T) {
	host := newIntegrationHost(t)
	host.audit = nil
	if _, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "missing-audit"}, host); err == nil || !strings.Contains(err.Error(), "shared Audit") {
		t.Fatalf("missing shared Audit appender error=%v", err)
	}
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
	if !ok || len(provider.HTTPAdapters()) != 1 || len(provider.HTTPAdapters()[0].Routes()) != 20 {
		t.Fatalf("Lifecycle HTTP adapters=%#v", provider)
	}
	if err := modulehttp.ValidateAdapter(provider.HTTPAdapters()[0]); err != nil {
		t.Fatal(err)
	}
	actionProvider, ok := binding.(actioncontract.Provider)
	if !ok {
		t.Fatal("Lifecycle binding does not expose its complete Action manifest")
	}
	actions, err := actionProvider.AuthorizationActions()
	if err != nil || len(actions) != 24 {
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
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ?", "workspace-a").Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("audit rows=%d err=%v", auditRows, err)
	}
	var retiredAuditTables int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_lifecycle_audit_evidence'").Scan(&retiredAuditTables); err != nil || retiredAuditTables != 0 {
		t.Fatalf("retired Lifecycle audit tables=%d err=%v", retiredAuditTables, err)
	}
	var migrationRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _schema_migrations").Scan(&migrationRows); err != nil || migrationRows != 3 {
		t.Fatalf("migration ledger rows=%d err=%v", migrationRows, err)
	}
	calls := host.registrar.snapshot()
	if len(calls) != 3 || calls[0].owner != "metadata" || calls[1].owner != migration.Owner || calls[2].owner != "metadata" || len(calls[1].migrations) != 2 {
		t.Fatalf("host migration registrations=%#v", calls)
	}
	for table, expected := range map[string]int{"_subject_requests": 1, "_lifecycle_subject_requests": 0} {
		var count int
		if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != expected {
			t.Fatalf("subject request table %s count=%d want=%d err=%v", table, count, expected, err)
		}
	}
	if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{}); err == nil {
		t.Fatal("Lifecycle owner extensions were rebound")
	}
}

func TestModuleSubjectExportBusinessContractEndToEnd(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "subject-business-contract"}, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	descriptor := binding.Descriptor()
	if err := descriptor.Validate(); err != nil {
		t.Fatal(err)
	}
	if descriptor.Mode != lifecyclesdk.DeploymentModeModule {
		t.Fatalf("Lifecycle deployment mode=%q", descriptor.Mode)
	}
	// Applying the same source-owned migration inventory through the host again
	// must reuse the host's only ledger instead of creating module-local state.
	reopened, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "subject-business-contract-reopen"}, host)
	if err != nil {
		t.Fatal(err)
	}
	_ = reopened.Close(t.Context())
	calls := host.registrar.snapshot()
	if len(calls) != 5 {
		t.Fatalf("host migration registrations=%d", len(calls))
	}
	for _, call := range calls {
		if call.owner != migration.Owner && call.owner != "metadata" {
			t.Fatalf("host migration registration=%#v", call)
		}
	}
	var migrationRows, migrationLedgers int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _schema_migrations").Scan(&migrationRows); err != nil || migrationRows != 3 {
		t.Fatalf("migration ledger rows=%d err=%v", migrationRows, err)
	}
	// SQLite's catalog is used only as dialect-focused integration evidence;
	// production migration DDL is still rendered and applied by domainry-orm.
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name LIKE '%schema_migrations'").Scan(&migrationLedgers); err != nil || migrationLedgers != 1 {
		t.Fatalf("migration ledger tables=%d err=%v", migrationLedgers, err)
	}

	previewReady := &sync.WaitGroup{}
	previewReady.Add(2)
	previewRelease := make(chan struct{})
	first := &recoverableSubjectHandler{owner: "first", previewReady: previewReady, previewRelease: previewRelease}
	second := &recoverableSubjectHandler{owner: "second", failFirst: true}
	owner := integrationOwner{}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{
		Executors:       []lifecyclecontract.OwnerLifecycleExecutor{owner},
		SubjectResolver: owner,
		SubjectHandlers: []lifecyclecontract.SubjectExecutionHandler{first, second},
		Artifacts:       artifacts,
	}); err != nil {
		t.Fatal(err)
	}
	governance := binding.Governance()
	if governance == nil {
		t.Fatal("Lifecycle Governance was not exposed after owner binding")
	}

	principalFor := func(userID, action string) lifecycleaccess.Principal {
		return lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: userID, Permissions: map[string]struct{}{action: {}}}
	}
	createPrincipal := principalFor("requester", lifecyclesdk.ActionLifecycleSubjectRequestsCreate)
	created, err := governance.CreateSubjectRequest(
		integrationIdentityContext(t.Context(), createPrincipal, identitysdk.DataScopeOwner),
		lifecyclemodel.SubjectRequest{WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, SubjectType: "user", SubjectID: "subject-1", Reason: "portability request"},
		createPrincipal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != lifecyclemodel.SubjectRequestPendingVerification || created.ID == "" || created.RequestedBy != "requester" {
		t.Fatalf("created subject request=%#v", created)
	}
	listPrincipal := principalFor("reviewer", lifecyclesdk.ActionLifecycleSubjectRequestsList)
	listed, err := governance.ListSubjectRequests(integrationIdentityContext(t.Context(), listPrincipal, identitysdk.DataScopeAll), 10, listPrincipal)
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed subject requests=%#v err=%v", listed, err)
	}
	readPrincipal := principalFor("reviewer", lifecyclesdk.ActionLifecycleSubjectRequestsRead)
	read, err := governance.GetSubjectRequest(integrationIdentityContext(t.Context(), readPrincipal, identitysdk.DataScopeAll), "workspace-a", created.ID, readPrincipal)
	if err != nil || read.ID != created.ID || read.SubjectID != "subject-1" {
		t.Fatalf("read subject request=%#v err=%v", read, err)
	}

	if _, err := governance.VerifySubjectRequest(integrationIdentityContext(t.Context(), createPrincipal, identitysdk.DataScopeOwner), "workspace-a", created.ID, "mfa-1", createPrincipal); err == nil || !strings.Contains(err.Error(), "auth.permission_denied") {
		t.Fatalf("another Action authorized verify: %v", err)
	}
	verifyPrincipal := principalFor("verifier", lifecyclesdk.ActionLifecycleSubjectRequestsVerify)
	verifyAllContext := integrationIdentityContext(t.Context(), verifyPrincipal, identitysdk.DataScopeAll)
	if _, err := governance.VerifySubjectRequest(verifyAllContext, "workspace-b", created.ID, "mfa-1", verifyPrincipal); err == nil || !strings.Contains(err.Error(), "auth.permission_denied") {
		t.Fatalf("cross-workspace verify error=%v", err)
	}
	ownerOnlyVerifier := principalFor("other-user", lifecyclesdk.ActionLifecycleSubjectRequestsVerify)
	if _, err := governance.VerifySubjectRequest(integrationIdentityContext(t.Context(), ownerOnlyVerifier, identitysdk.DataScopeOwner), "workspace-a", created.ID, "mfa-1", ownerOnlyVerifier); err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("owner-scoped verifier accessed another user's request: %v", err)
	}
	verified, err := governance.VerifySubjectRequest(verifyAllContext, "workspace-a", created.ID, "mfa-1", verifyPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Status != lifecyclemodel.SubjectRequestVerified || verified.ResolvedIdentity != "subject-1" || verified.VerifiedBy != "verifier" {
		t.Fatalf("verified subject request=%#v", verified)
	}

	previewPrincipal := principalFor("reviewer", lifecyclesdk.ActionLifecycleSubjectRequestsPreview)
	previewContext := integrationIdentityContext(t.Context(), previewPrincipal, identitysdk.DataScopeAll)
	type previewOutcome struct {
		request lifecyclemodel.SubjectRequest
		err     error
	}
	previewOutcomes := make(chan previewOutcome, 2)
	for range 2 {
		go func() {
			request, previewErr := governance.PreviewSubjectRequest(previewContext, "workspace-a", created.ID, previewPrincipal)
			previewOutcomes <- previewOutcome{request: request, err: previewErr}
		}()
	}
	previewReady.Wait()
	close(previewRelease)
	var previewed lifecyclemodel.SubjectRequest
	previewSuccesses, previewConflicts := 0, 0
	for range 2 {
		outcome := <-previewOutcomes
		if outcome.err != nil {
			previewConflicts++
			continue
		}
		previewSuccesses++
		previewed = outcome.request
	}
	if previewSuccesses != 1 || previewConflicts != 1 || previewed.Status != lifecyclemodel.SubjectRequestPreviewed {
		t.Fatalf("concurrent preview outcomes successes=%d conflicts=%d request=%#v", previewSuccesses, previewConflicts, previewed)
	}
	var previewAudits int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND event = ? AND record_id = ?", "workspace-a", "lifecycle.subject.previewed", created.ID).Scan(&previewAudits); err != nil || previewAudits != 1 {
		t.Fatalf("preview audit rows=%d err=%v", previewAudits, err)
	}
	var subjectAuditAfter string
	if err := host.db.QueryRowContext(t.Context(), "SELECT after_json FROM _audit_events WHERE workspace_id = ? AND event = ? AND record_id = ?", "workspace-a", "lifecycle.subject.verified", created.ID).Scan(&subjectAuditAfter); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(subjectAuditAfter, "subject-1") || strings.Contains(subjectAuditAfter, "mfa-1") || !strings.Contains(subjectAuditAfter, `"status":"verified"`) {
		t.Fatalf("subject Audit projection leaked private workflow fields: %s", subjectAuditAfter)
	}

	selfApprover := principalFor("requester", lifecyclesdk.ActionLifecycleSubjectRequestsApprove)
	if _, err := governance.ApproveSubjectRequest(integrationIdentityContext(t.Context(), selfApprover, identitysdk.DataScopeAll), "workspace-a", created.ID, selfApprover); err == nil {
		t.Fatal("requester self-approval was accepted")
	}
	approver := principalFor("approver", lifecyclesdk.ActionLifecycleSubjectRequestsApprove)
	approved, err := governance.ApproveSubjectRequest(integrationIdentityContext(t.Context(), approver, identitysdk.DataScopeAll), "workspace-a", created.ID, approver)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != lifecyclemodel.SubjectRequestApproved || approved.ApprovedBy != "approver" {
		t.Fatalf("approved subject request=%#v", approved)
	}

	executor := principalFor("operator", lifecyclesdk.ActionLifecycleSubjectRequestsExecute)
	executeContext := integrationIdentityContext(t.Context(), executor, identitysdk.DataScopeAll)
	failed, err := governance.ExecuteSubjectRequest(executeContext, "workspace-a", created.ID, executor)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != lifecyclemodel.SubjectRequestFailed || failed.ExecutionAttempt != 1 || first.exportAttempts != 1 || second.exportAttempts != 1 {
		t.Fatalf("first execution request=%#v attempts=(%d,%d)", failed, first.exportAttempts, second.exportAttempts)
	}
	succeeded, err := governance.ExecuteSubjectRequest(executeContext, "workspace-a", created.ID, executor)
	if err != nil {
		t.Fatal(err)
	}
	if succeeded.Status != lifecyclemodel.SubjectRequestSucceeded || succeeded.ExecutionAttempt != 2 || first.exportAttempts != 1 || second.exportAttempts != 2 {
		t.Fatalf("retried execution request=%#v attempts=(%d,%d)", succeeded, first.exportAttempts, second.exportAttempts)
	}
	artifact, found, err := host.artifacts.ByID(t.Context(), "workspace-a", succeeded.ResultReference)
	if err != nil || !found || artifact.Owner != sharedartifact.OwnerLifecycle || artifact.Kind != "subject_export" || artifact.Status != sharedartifact.StatusAvailable || !artifact.ExpiresAt.Equal(succeeded.DownloadExpiresAt) {
		t.Fatalf("shared subject export Artifact=%#v found=%v err=%v", artifact, found, err)
	}
	bindings, err := host.artifacts.Bindings(t.Context(), "workspace-a", artifact.ID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("shared subject export bindings=%#v err=%v", bindings, err)
	}
	bound := map[string]string{}
	for _, binding := range bindings {
		bound[binding.Kind] = binding.ResourceID
	}
	if bound[sharedartifact.BindingJob] != succeeded.ID || bound[sharedartifact.BindingSubject] != succeeded.ResolvedIdentity {
		t.Fatalf("shared subject export binding identities=%#v", bound)
	}
	contentInfo, err := host.content.Stat(t.Context(), "workspace-a", artifact.StorageReference)
	if err != nil || contentInfo.SHA256 != artifact.ContentSHA256 || contentInfo.Size != artifact.SizeBytes {
		t.Fatalf("shared subject export content=%#v err=%v", contentInfo, err)
	}
	if _, err := governance.ExecuteSubjectRequest(executeContext, "workspace-a", created.ID, executor); err == nil {
		t.Fatal("terminal subject request accepted another execution transition")
	}
	if first.exportAttempts != 1 || second.exportAttempts != 2 {
		t.Fatalf("terminal replay repeated owner effects: attempts=(%d,%d)", first.exportAttempts, second.exportAttempts)
	}

	downloadPrincipal := principalFor("requester", lifecyclesdk.ActionLifecycleSubjectExportsDownload)
	payload, err := governance.DownloadSubjectExport(integrationIdentityContext(t.Context(), downloadPrincipal, identitysdk.DataScopeOwner), "workspace-a", created.ID, downloadPrincipal, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var ownerPayloads map[string]json.RawMessage
	if err := json.Unmarshal(payload, &ownerPayloads); err != nil || len(ownerPayloads) != 3 {
		t.Fatalf("downloaded owner payloads=%s err=%v", payload, err)
	}
	var persistedStatus string
	var persistedPayload string
	if err := host.db.QueryRowContext(t.Context(), "SELECT status, payload_json FROM _subject_requests WHERE workspace_id = ? AND id = ?", "workspace-a", created.ID).Scan(&persistedStatus, &persistedPayload); err != nil {
		t.Fatal(err)
	}
	var persisted lifecyclemodel.SubjectRequest
	if err := json.Unmarshal([]byte(persistedPayload), &persisted); err != nil {
		t.Fatal(err)
	}
	if persistedStatus != string(lifecyclemodel.SubjectRequestSucceeded) ||
		persisted.Status != lifecyclemodel.SubjectRequestSucceeded ||
		persisted.ResolvedIdentity != "subject-1" ||
		persisted.RequestedBy != "requester" ||
		persisted.VerifiedBy != "verifier" ||
		persisted.ApprovedBy != "approver" ||
		persisted.SecondFactorRef != "mfa-1" ||
		persisted.Reason != "portability request" ||
		len(persisted.ImpactPreview) == 0 ||
		persisted.ResultReference == "" ||
		!persisted.DownloadExpiresAt.After(persisted.UpdatedAt) ||
		persisted.ExecutionAttempt != 2 {
		t.Fatalf("persisted subject request=%#v status=%q", persisted, persistedStatus)
	}
	var stepRows, auditRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _subject_steps WHERE workspace_id = ? AND request_id = ?", "workspace-a", created.ID).Scan(&stepRows); err != nil || stepRows != 3 {
		t.Fatalf("execution step rows=%d err=%v", stepRows, err)
	}
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND record_id = ?", "workspace-a", created.ID).Scan(&auditRows); err != nil || auditRows != 9 {
		t.Fatalf("subject audit rows=%d err=%v", auditRows, err)
	}
}

func TestModuleMutationRollsBackOnHostTransactionFailure(t *testing.T) {
	host := newIntegrationHost(t)
	host.transactions = &rollbackOnceIntegrationTransactor{db: host.db, pending: true}
	binding, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "transaction-rollback"}, host)
	if err != nil {
		t.Fatal(err)
	}
	owner := integrationOwner{}
	artifacts, err := binding.SubjectArtifacts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.BindOwners(t.Context(), lifecyclesdk.OwnerExtensions{Executors: []lifecyclecontract.OwnerLifecycleExecutor{owner}, SubjectResolver: owner, SubjectHandlers: []lifecyclecontract.SubjectExecutionHandler{owner}, Artifacts: artifacts}); err != nil {
		t.Fatal(err)
	}
	publisher := lifecycleaccess.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "publisher", Permissions: map[string]struct{}{lifecyclesdk.ActionLifecyclePoliciesPublish: {}}}
	ctx := integrationIdentityContext(t.Context(), publisher, identitysdk.DataScopeAll)
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "rollback.v1", Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct, DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, WorkspaceMayExtend: true, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}}
	if _, err := binding.Governance().PublishPolicy(ctx, policy, publisher); err == nil {
		t.Fatal("host transaction rollback was hidden")
	} else {
		var sdkErr *lifecyclesdk.Error
		if !errors.As(err, &sdkErr) || sdkErr.Cause == nil || !strings.Contains(sdkErr.Cause.Error(), "forced transaction rollback") {
			t.Fatalf("forced rollback error=%#v", err)
		}
	}
	var policyRows, auditRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _definitions WHERE owner = ? AND kind = ? AND source_id = ? AND name = ?", metadatasdk.DefinitionOwnerLifecycle, "retention_policy", "workspace-a", policy.Policy.Key).Scan(&policyRows); err != nil {
		t.Fatal(err)
	}
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND record_id = ?", "workspace-a", policy.Policy.Key).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if policyRows != 0 || auditRows != 0 {
		t.Fatalf("rolled-back rows policy=%d audit=%d", policyRows, auditRows)
	}
	created, err := binding.Governance().PublishPolicy(ctx, policy, publisher)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != lifecyclemodel.PolicyStatusPublished || created.Revision != 1 {
		t.Fatalf("retried policy publication=%#v", created)
	}
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _definitions WHERE owner = ? AND kind = ? AND source_id = ? AND name = ?", metadatasdk.DefinitionOwnerLifecycle, "retention_policy", "workspace-a", policy.Policy.Key).Scan(&policyRows); err != nil || policyRows != 1 {
		t.Fatalf("retried policy rows=%d err=%v", policyRows, err)
	}
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND record_id = ?", "workspace-a", policy.Policy.Key).Scan(&auditRows); err != nil || auditRows != 1 {
		t.Fatalf("retried audit rows=%d err=%v", auditRows, err)
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
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _subject_steps WHERE workspace_id = ? AND request_id = ?", "workspace-a", request.ID).Scan(&stepRows); err != nil || stepRows != 3 {
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
	store := lifecyclestore.NewLifecycleStore(host, host.definitions)
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	for _, request := range []internalmodel.SubjectRequest{
		{ID: "request-a", WorkspaceID: "workspace-a", Kind: internalmodel.SubjectRequestErase, Status: internalmodel.SubjectRequestSucceeded, SubjectID: "subject-a", ResolvedIdentity: "identity-a", RequestedBy: "requester", BackupPending: true, ResultReference: "erase-evidence:request-a", CreatedAt: now, UpdatedAt: now},
		{ID: "request-b", WorkspaceID: "workspace-a", Kind: internalmodel.SubjectRequestErase, Status: internalmodel.SubjectRequestSucceeded, SubjectID: "subject-b", ResolvedIdentity: "identity-b", RequestedBy: "requester", BackupPending: true, ResultReference: "erase-evidence:request-b", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveSubjectRequest(t.Context(), request); err != nil {
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
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND event = ?", "workspace-a", "lifecycle.deletion.replayed").Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("replay audit rows=%d err=%v", audits, err)
	}
}
