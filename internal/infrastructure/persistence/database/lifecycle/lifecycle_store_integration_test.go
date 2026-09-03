package lifecycle

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodel "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/model"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/internal/domain/lifecycle/repository"
	schema "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/schema"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	_ "modernc.org/sqlite"
)

type persistenceTestHost struct {
	db      *sql.DB
	dialect modulehost.Dialect
}

func (h persistenceTestHost) Database() modulehost.Database           { return h.db }
func (h persistenceTestHost) Dialect() modulehost.Dialect             { return h.dialect }
func (persistenceTestHost) Migrations() modulehost.MigrationRegistrar { return nil }
func (persistenceTestHost) Transactions() modulehost.Transactor       { return nil }

func TestLifecycleStorePersistsOwnedAggregate(t *testing.T) {
	repository, db := newPersistenceTestStore(t)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	policy := lifecyclemodel.PolicyVersion{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "record.v1", Version: "1", Owner: "record"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: now}
	if err := repository.SavePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.LatestPolicy(t.Context(), "workspace-a", "record.v1", lifecyclepersistence.UnrestrictedDataScopeFilter())
	if err != nil || !found || loaded.Policy.Version != "1" {
		t.Fatalf("loaded=%#v found=%t err=%v", loaded, found, err)
	}
	evidence := lifecyclemodel.AuditEvidence{ID: "evidence-1", WorkspaceID: "workspace-a", Event: "test", ResourceID: "record-1", CreatedAt: now}
	if err := repository.AppendAuditEvidence(context.Background(), evidence); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _lifecycle_audit_evidence WHERE workspace_id = ?", "workspace-a").Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit evidence count=%d err=%v", count, err)
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
	return NewLifecycleStore(persistenceTestHost{db: db, dialect: renderer}), db
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

	for _, registration := range []lifecyclemodel.DeletionRegistration{
		{RequestID: "request-a", WorkspaceID: "workspace-a", ResolvedIdentity: "identity-a", BackupPending: true, UpdatedAt: now},
		{RequestID: "request-b", WorkspaceID: "workspace-a", ResolvedIdentity: "identity-b", BackupPending: true, UpdatedAt: now},
	} {
		if err := repository.SaveDeletionRegistration(t.Context(), registration); err != nil {
			t.Fatal(err)
		}
	}
	registrations, err := repository.ListPendingDeletionRegistrations(t.Context(), "workspace-a", 10, ownerA)
	if err != nil || len(registrations) != 1 || registrations[0].RequestID != "request-a" {
		t.Fatalf("scoped deletion registrations=%#v err=%v", registrations, err)
	}

	for _, job := range []lifecyclemodel.CleanupJob{
		{ID: "job-a", WorkspaceID: "workspace-a", PolicyKey: "policy-a", PolicyVersion: "1", Status: lifecyclemodel.CleanupStatusPending, RequestedBy: "user-a", OwnerOrgID: "org-a", CreatedAt: now, UpdatedAt: now},
		{ID: "job-b", WorkspaceID: "workspace-a", PolicyKey: "policy-b", PolicyVersion: "1", Status: lifecyclemodel.CleanupStatusPending, RequestedBy: "user-b", OwnerOrgID: "org-b", CreatedAt: now, UpdatedAt: now},
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
	if err != nil || metrics.EligibleBacklog != 1 || metrics.LegalHoldCount != 1 {
		t.Fatalf("scoped metrics=%#v err=%v", metrics, err)
	}
}
