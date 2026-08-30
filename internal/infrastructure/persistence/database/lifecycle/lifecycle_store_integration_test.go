package lifecycle

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
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
	for _, statement := range values[0].Statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	repository := NewLifecycleStore(persistenceTestHost{db: db, dialect: renderer})
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	policy := lifecyclemodel.PolicyVersion{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "record.v1", Version: "1", Owner: "record"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: now}
	if err := repository.SavePolicy(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.LatestPolicy(t.Context(), "workspace-a", "record.v1")
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
