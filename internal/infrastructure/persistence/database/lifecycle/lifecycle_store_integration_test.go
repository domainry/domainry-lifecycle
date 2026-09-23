package lifecycle

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
	schema "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/schema"
	artifactfixture "github.com/domainry/domainry-lifecycle/internal/testsupport/artifactfixture"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	_ "modernc.org/sqlite"
)

type persistenceUploadFields struct{}

func (persistenceUploadFields) HasUploadField(string, string) bool { return true }

func TestFileScanQueueSurvivesRestartAndTerminalEvidenceIsImmutable(t *testing.T) {
	repository, _ := newPersistenceTestStore(t)
	store := NewFileArtifactStore(repository.host, persistenceUploadFields{}, t.TempDir())
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, artifact := range []lifecyclecontract.UploadArtifact{
		{ID: "file-b", WorkspaceID: "workspace-b", ObjectKey: "document", FieldKey: "file", Filename: "b.png", ContentType: "image/png", SHA256: "bbb", Size: 20, CreatedAt: now.Add(time.Second)},
		{ID: "file-a", WorkspaceID: "workspace-a", ObjectKey: "document", FieldKey: "file", Filename: "a.png", ContentType: "image/png", SHA256: "aaa", Size: 10, CreatedAt: now},
	} {
		if err := store.RegisterUpload(t.Context(), artifact); err != nil {
			t.Fatal(err)
		}
	}
	scope := lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "scan pending uploads")
	pending, err := store.PendingFileScans(t.Context(), scope, 25)
	if err != nil || len(pending) != 2 || pending[0].FileID != "file-a" || pending[1].FileID != "file-b" {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	clean := pending[0]
	clean.Status, clean.Provider, clean.EvidenceRef, clean.ScannedAt = lifecyclecontract.FileScanClean, "scanner-v1", "sha256:aaa", now.Add(time.Minute)
	if err := store.RecordFileScan(t.Context(), clean); err != nil {
		t.Fatal(err)
	}
	// A restarted worker may replay the same deterministic terminal result.
	clean.ScannedAt = now.Add(2 * time.Minute)
	if err := store.RecordFileScan(t.Context(), clean); err != nil {
		t.Fatalf("same terminal replay failed: %v", err)
	}
	conflict := clean
	conflict.Status, conflict.EvidenceRef = lifecyclecontract.FileScanQuarantined, "sha256:different"
	if err := store.RecordFileScan(t.Context(), conflict); err == nil {
		t.Fatal("conflicting terminal scan evidence replaced the committed result")
	}
	pending, err = store.PendingFileScans(t.Context(), scope, 25)
	if err != nil || len(pending) != 1 || pending[0].FileID != "file-b" {
		t.Fatalf("remaining pending=%#v err=%v", pending, err)
	}
}

func TestUploadRegistrationIsIdentityIdempotentAndNeverResetsTerminalScan(t *testing.T) {
	repository, _ := newPersistenceTestStore(t)
	store := NewFileArtifactStore(repository.host, persistenceUploadFields{}, t.TempDir())
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	artifact := lifecyclecontract.UploadArtifact{ID: "derived-1", WorkspaceID: "workspace-a", ObjectKey: "document", FieldKey: "file", Filename: "derived.pdf", ContentType: "application/pdf", SHA256: "aaa", Size: 10, CreatedAt: now}
	if err := store.RegisterUpload(t.Context(), artifact); err != nil {
		t.Fatal(err)
	}
	clean, err := store.FindFileScan(t.Context(), artifact.WorkspaceID, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	clean.Status, clean.Provider, clean.EvidenceRef, clean.ScannedAt = lifecyclecontract.FileScanClean, "derived-v1", "sha256:aaa", now.Add(time.Minute)
	if err := store.RecordFileScan(t.Context(), clean); err != nil {
		t.Fatal(err)
	}
	artifact.CreatedAt = now.Add(time.Hour)
	if err := store.RegisterUpload(t.Context(), artifact); err != nil {
		t.Fatalf("identical retry failed: %v", err)
	}
	after, err := store.FindFileScan(t.Context(), artifact.WorkspaceID, artifact.ID)
	if err != nil || after.Status != lifecyclecontract.FileScanClean || after.Provider != "derived-v1" || !after.ScannedAt.Equal(clean.ScannedAt) {
		t.Fatalf("terminal evidence reset: after=%+v err=%v", after, err)
	}
	conflict := artifact
	conflict.SHA256 = "bbb"
	if err := store.RegisterUpload(t.Context(), conflict); !errors.Is(err, errUploadArtifactIdentityConflict) {
		t.Fatalf("expected identity conflict, got %v", err)
	}
}

func TestLegalHoldsRemainActiveConstraintsAndCleanupJobsRetainFencedCursor(t *testing.T) {
	repository, _ := newPersistenceTestStore(t)
	now := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	endedAt := now.Add(-time.Minute)
	for _, hold := range []lifecyclemodel.LegalHold{
		{ID: "active-hold", WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-1", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour)},
		{ID: "expired-hold", WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-1", StartsAt: now.Add(-2 * time.Hour), EndsAt: &endedAt, ReviewAt: now},
		{ID: "other-resource", WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-2", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour)},
	} {
		if err := repository.SaveLegalHold(t.Context(), hold); err != nil {
			t.Fatal(err)
		}
	}
	holds, err := repository.ActiveLegalHolds(t.Context(), lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-1"}, now)
	if err != nil || len(holds) != 1 || holds[0].ID != "active-hold" {
		t.Fatalf("active legal holds=%#v err=%v", holds, err)
	}

	job := lifecyclemodel.CleanupJob{
		ID: "cleanup-1", WorkspaceID: "workspace-a", PolicyKey: "records", PolicyVersion: "1", Status: lifecyclemodel.CleanupStatusPending,
		RequestedBy: "operator", Checkpoint: "page-1", CreatedAt: now, UpdatedAt: now,
	}
	if err := repository.SaveCleanupJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	runnable, err := repository.ListRunnableCleanupJobs(t.Context(), lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "test cleanup queue"), 10, now)
	if err != nil || len(runnable) != 1 || runnable[0].ID != job.ID {
		t.Fatalf("runnable cleanup jobs=%#v err=%v", runnable, err)
	}
	claimContext := requestcontext.WithOwnerExecutionID(t.Context(), "operation-cleanup")
	first, acquired, err := repository.ClaimCleanupJob(claimContext, job.WorkspaceID, job.ID, "worker-a", time.Minute, now)
	if err != nil || !acquired || first.OperationID != "operation-cleanup" || first.FencingToken != 1 || first.LeaseOwner != "worker-a" || first.Checkpoint != "page-1" {
		t.Fatalf("first claim=%#v acquired=%v err=%v", first, acquired, err)
	}
	if _, acquired, err := repository.ClaimCleanupJob(t.Context(), job.WorkspaceID, job.ID, "worker-b", time.Minute, now.Add(30*time.Second)); err != nil || acquired {
		t.Fatalf("concurrent claim acquired=%v err=%v", acquired, err)
	}
	second, acquired, err := repository.ClaimCleanupJob(t.Context(), job.WorkspaceID, job.ID, "worker-b", time.Minute, now.Add(2*time.Minute))
	if err != nil || !acquired || second.FencingToken != 2 || second.LeaseOwner != "worker-b" {
		t.Fatalf("reclaimed job=%#v acquired=%v err=%v", second, acquired, err)
	}
	first.Checkpoint, first.UpdatedAt = "stale-page", now.Add(2*time.Minute)
	if err := repository.UpdateCleanupJob(t.Context(), first); err == nil {
		t.Fatal("stale cleanup worker overwrote a reclaimed job")
	}
	second.Checkpoint, second.Status, second.LeaseOwner, second.LeaseExpiresAt, second.UpdatedAt = "page-2", lifecyclemodel.CleanupStatusPending, "", time.Time{}, now.Add(2*time.Minute)
	if err := repository.UpdateCleanupJob(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	persisted, found, err := repository.GetCleanupJob(t.Context(), job.WorkspaceID, job.ID)
	if err != nil || !found || persisted.OperationID != "operation-cleanup" || persisted.Checkpoint != "page-2" || persisted.FencingToken != 2 || persisted.Status != lifecyclemodel.CleanupStatusPending {
		t.Fatalf("persisted cleanup job=%#v found=%v err=%v", persisted, found, err)
	}
}

type persistenceTestHost struct {
	db          *sql.DB
	dialect     modulehost.Dialect
	definitions metadatasdk.DefinitionStore
	artifacts   *artifactfixture.Store
	content     *artifactfixture.Content
}

func (h persistenceTestHost) Database() modulehost.Database           { return h.db }
func (h persistenceTestHost) Dialect() modulehost.Dialect             { return h.dialect }
func (persistenceTestHost) Migrations() modulehost.MigrationRegistrar { return nil }
func (persistenceTestHost) Transactions() modulehost.Transactor       { return nil }
func (h persistenceTestHost) DefinitionStore() metadatasdk.DefinitionStore {
	return h.definitions
}
func (h persistenceTestHost) ArtifactStore() sharedartifact.ManagedStore { return h.artifacts }
func (h persistenceTestHost) ArtifactContentStore() lifecyclecontract.ArtifactContentStore {
	return h.content
}
func (h persistenceTestHost) ArtifactContentWriter() lifecyclecontract.ArtifactContentWriter {
	return h.content
}

func TestLifecycleStorePersistsOwnedAggregate(t *testing.T) {
	repository, db := newPersistenceTestStore(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	policy := lifecyclemodel.PolicyVersion{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "record.v1", Version: "1", Owner: "record"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedBy: "publisher", PublishedAt: now}
	if err := repository.SavePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.LatestPolicy(t.Context(), "workspace-a", "record.v1", lifecyclepersistence.UnrestrictedDataScopeFilter())
	if err != nil || !found || loaded.Policy.Version != "1" {
		t.Fatalf("loaded=%#v found=%t err=%v", loaded, found, err)
	}
	definitionKey := retentionPolicyDefinitionKey(policy.WorkspaceID, policy.Policy.Key)
	firstDefinition, found, err := repository.definitions.Get(t.Context(), metadatasdk.DefinitionOwnerLifecycle, retentionPolicyDefinitionKind, definitionKey)
	if err != nil || !found || firstDefinition.CurrentVersionID == "" {
		t.Fatalf("first policy Definition=%#v found=%t err=%v", firstDefinition, found, err)
	}
	next := policy
	next.Policy.Version = "2"
	next.Revision = 2
	next.PublishedAt = now.Add(time.Minute)
	if err := repository.SavePolicy(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	loaded, found, err = repository.LatestPolicy(t.Context(), "workspace-a", "record.v1", lifecyclepersistence.UnrestrictedDataScopeFilter())
	if err != nil || !found || loaded.Policy.Version != "2" || loaded.Revision != 2 {
		t.Fatalf("updated=%#v found=%t err=%v", loaded, found, err)
	}
	if firstVersion, found, err := repository.definitions.GetVersion(t.Context(), metadatasdk.DefinitionVersionQuery{
		Owner: metadatasdk.DefinitionOwnerLifecycle, ResourceType: retentionPolicyDefinitionKind,
		ResourceKey: definitionKey, VersionID: firstDefinition.CurrentVersionID,
	}); err != nil || !found || len(firstVersion.Payload) == 0 {
		t.Fatalf("immutable first policy version=%#v found=%t err=%v", firstVersion, found, err)
	}
	if err := repository.SavePolicy(t.Context(), next); err == nil {
		t.Fatal("stale lifecycle policy revision was accepted")
	}
	for _, retired := range []string{"_lifecycle_audit_evidence", "_lifecycle_archive_entries", "_lifecycle_file_artifacts"} {
		var count int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", retired).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired Lifecycle table %s count=%d err=%v", retired, count, err)
		}
	}
}

func newPersistenceTestStore(t *testing.T) (LifecycleStore, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	renderer := dialect.WithSchema("")
	values, err := schema.Migrations(renderer)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range values {
		for _, statement := range migration.Statements {
			if _, err := db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	return NewLifecycleStore(persistenceTestHost{
		db: db, dialect: renderer, definitions: newMemoryDefinitionStore(), artifacts: artifactfixture.NewStore(), content: artifactfixture.NewContent(),
	}), db
}

func TestLifecycleStorePushesScopeIntoBusinessAndRelatedResourceQueries(t *testing.T) {
	repository, _ := newPersistenceTestStore(t)
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	ownerA := lifecyclepersistence.DataScopeFilter{OwnerUserIDs: []string{"user-a"}}
	all := lifecyclepersistence.UnrestrictedDataScopeFilter()

	for _, policy := range []lifecyclemodel.PolicyVersion{
		{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "policy-a", Version: "1", Owner: "record"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedBy: "user-a", OwnerOrgID: "org-a", PublishedAt: now},
		{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "policy-b", Version: "1", Owner: "record"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedBy: "user-b", OwnerOrgID: "org-b", PublishedAt: now},
	} {
		if err := repository.SavePolicy(t.Context(), policy); err != nil {
			t.Fatal(err)
		}
	}
	policies, err := repository.ListPolicies(t.Context(), "workspace-a", ownerA)
	if err != nil || len(policies) != 1 || policies[0].Policy.Key != "policy-a" {
		t.Fatalf("owner policies=%#v err=%v", policies, err)
	}
	policies, err = repository.ListPolicies(t.Context(), "workspace-a", lifecyclepersistence.DataScopeFilter{OwnerOrgIDs: []string{"org-b"}})
	if err != nil || len(policies) != 1 || policies[0].Policy.Key != "policy-b" {
		t.Fatalf("organization policies=%#v err=%v", policies, err)
	}
	policies, err = repository.ListPolicies(t.Context(), "workspace-a", all)
	if err != nil || len(policies) != 2 {
		t.Fatalf("all policies=%#v err=%v", policies, err)
	}

	holdA := lifecyclemodel.LegalHold{ID: "hold-a", WorkspaceID: "workspace-a", CreatedBy: "user-a", OwnerOrgID: "org-a", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour)}
	holdB := lifecyclemodel.LegalHold{ID: "hold-b", WorkspaceID: "workspace-a", CreatedBy: "user-b", OwnerOrgID: "org-b", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour)}
	if err := repository.SaveLegalHold(t.Context(), holdA); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveLegalHold(t.Context(), holdB); err != nil {
		t.Fatal(err)
	}
	holds, err := repository.ListLegalHolds(t.Context(), "workspace-a", 100, ownerA)
	if err != nil || len(holds) != 1 || holds[0].ID != "hold-a" {
		t.Fatalf("owner legal holds=%#v err=%v", holds, err)
	}
	holds, err = repository.ListLegalHolds(t.Context(), "workspace-a", 1, all)
	if err != nil || len(holds) != 1 || holds[0].ID != "hold-b" {
		t.Fatalf("limited legal holds=%#v err=%v", holds, err)
	}
	unauthorized := holdB
	endedAt := now.Add(30 * time.Minute)
	unauthorized.EndsAt = &endedAt
	if updated, err := repository.UpdateLegalHold(t.Context(), unauthorized, ownerA); err != nil || updated {
		t.Fatalf("cross-owner legal-hold update=%t err=%v", updated, err)
	}
	loadedHold, found, err := repository.GetLegalHold(t.Context(), "workspace-a", "hold-b", all)
	if err != nil || !found || loadedHold.EndsAt != nil {
		t.Fatalf("cross-owner write changed hold=%#v found=%t err=%v", loadedHold, found, err)
	}

	requests := []lifecyclemodel.SubjectRequest{
		{ID: "request-a", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, Status: lifecyclemodel.SubjectRequestPendingVerification, SubjectID: "subject-a", RequestedBy: "user-a", OwnerOrgID: "org-a", CreatedAt: now, UpdatedAt: now},
		{ID: "request-b", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, Status: lifecyclemodel.SubjectRequestPendingVerification, SubjectID: "subject-b", RequestedBy: "user-b", OwnerOrgID: "org-b", CreatedAt: now, UpdatedAt: now},
	}
	for _, request := range requests {
		if err := repository.SaveSubjectRequest(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	unauthorizedTransition := requests[1]
	unauthorizedTransition.Status = lifecyclemodel.SubjectRequestVerified
	unauthorizedTransition.ResolvedIdentity, unauthorizedTransition.VerifiedBy, unauthorizedTransition.SecondFactorRef = "identity-b", "verifier", "mfa"
	unauthorizedTransition.UpdatedAt = now.Add(time.Minute)
	if err := repository.TransitionSubjectRequest(t.Context(), requests[1], unauthorizedTransition, ownerA); err == nil {
		t.Fatal("cross-owner subject transition was accepted")
	}
	loadedRequest, found, err := repository.GetSubjectRequest(t.Context(), "workspace-a", "request-b", all)
	if err != nil || !found || loadedRequest.Status != lifecyclemodel.SubjectRequestPendingVerification {
		t.Fatalf("cross-owner transition changed request=%#v found=%t err=%v", loadedRequest, found, err)
	}
	for _, item := range []lifecyclemodel.ExternalErasure{
		{ID: "external-a", RequestID: "request-a", WorkspaceID: "workspace-a", Status: "pending"},
		{ID: "external-b", RequestID: "request-b", WorkspaceID: "workspace-a", Status: "pending"},
	} {
		if err := repository.SaveExternalErasures(t.Context(), []lifecyclemodel.ExternalErasure{item}); err != nil {
			t.Fatal(err)
		}
	}
	external, err := repository.ListExternalErasures(t.Context(), "workspace-a", "", ownerA)
	if err != nil || len(external) != 1 || external[0].ID != "external-a" {
		t.Fatalf("scoped external erasures=%#v err=%v", external, err)
	}
	if _, found, err := repository.ReconcileExternalErasure(t.Context(), "workspace-a", "external-b", "evidence", now, ownerA); err != nil || found {
		t.Fatalf("cross-owner reconciliation found=%t err=%v", found, err)
	}

	for index := range requests {
		request := requests[index]
		request.Status = lifecyclemodel.SubjectRequestSucceeded
		request.ResolvedIdentity = []string{"identity-a", "identity-b"}[index]
		request.BackupPending = true
		request.ResultReference = "erase-evidence:" + request.ID
		request.UpdatedAt = now.Add(time.Minute)
		if err := repository.SaveSubjectRequest(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	registrations, err := repository.ListPendingDeletionRegistrations(t.Context(), "workspace-a", 10, ownerA)
	if err != nil || len(registrations) != 1 || registrations[0].RequestID != "request-a" {
		t.Fatalf("scoped deletion registrations=%#v err=%v", registrations, err)
	}

	for _, job := range []lifecyclemodel.CleanupJob{
		{ID: "job-a", WorkspaceID: "workspace-a", PolicyKey: "policy-a", PolicyVersion: "1", Status: lifecyclemodel.CleanupStatusFailed, Purged: 7, RequestedBy: "user-a", OwnerOrgID: "org-a", CreatedAt: now, UpdatedAt: now},
		{ID: "job-b", WorkspaceID: "workspace-a", PolicyKey: "policy-b", PolicyVersion: "1", Status: lifecyclemodel.CleanupStatusFailed, Purged: 99, RequestedBy: "user-b", OwnerOrgID: "org-b", CreatedAt: now, UpdatedAt: now},
	} {
		if err := repository.SaveCleanupJob(t.Context(), job); err != nil {
			t.Fatal(err)
		}
	}
	writer := NewArchiveWriter(repository.host)
	for _, value := range []struct {
		jobID, policyKey string
	}{
		{jobID: "job-a", policyKey: "policy-a"},
		{jobID: "job-b", policyKey: "policy-b"},
	} {
		job, found, err := repository.GetCleanupJob(t.Context(), "workspace-a", value.jobID)
		if err != nil || !found {
			t.Fatalf("job=%#v found=%t err=%v", job, found, err)
		}
		policy, found, err := repository.LatestPolicy(t.Context(), "workspace-a", value.policyKey, all)
		if err != nil || !found {
			t.Fatalf("policy=%#v found=%t err=%v", policy, found, err)
		}
		if _, err := writer.ArchivePayload(t.Context(), "record", job, policy, "records", "resource-"+value.jobID, []byte(value.jobID)); err != nil {
			t.Fatal(err)
		}
	}
	archives, err := repository.ListArchiveEntries(t.Context(), "workspace-a", "", 10, ownerA)
	if err != nil || len(archives) != 1 || archives[0].JobID != "job-a" {
		t.Fatalf("scoped archives=%#v err=%v", archives, err)
	}
	metrics, err := repository.Metrics(t.Context(), "workspace-a", now, ownerA)
	if err != nil || metrics.EligibleBacklog != 1 || metrics.LegalHoldCount != 1 || metrics.FailureTotal != 1 || metrics.PurgedTotal != 7 {
		t.Fatalf("scoped metrics=%#v err=%v", metrics, err)
	}
}
