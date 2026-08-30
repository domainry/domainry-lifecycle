package module

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	schema "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/schema"
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
	if owner != schema.Owner {
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

func TestBindOwnsPersistenceAndUsesHostTransaction(t *testing.T) {
	host := newIntegrationHost(t)
	binding, err := NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: "integration-test"}, host)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	policy := lifecyclemodel.PolicyVersion{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "records.v1", Version: "1", Owner: "record"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: now}
	if err := binding.WithinTransaction(t.Context(), func(ctx context.Context) error {
		return binding.Repository().SavePolicy(ctx, policy)
	}); err != nil {
		t.Fatal(err)
	}
	if policies, err := binding.Repository().ListPolicies(t.Context(), "workspace-a"); err != nil || len(policies) != 1 {
		t.Fatalf("committed policies=%#v err=%v", policies, err)
	}
	wantRollback := errors.New("rollback")
	policy.Policy.Version, policy.Revision = "2", 2
	if err := binding.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if err := binding.Repository().SavePolicy(ctx, policy); err != nil {
			return err
		}
		return wantRollback
	}); !errors.Is(err, wantRollback) {
		t.Fatalf("rollback error=%v", err)
	}
	if policies, err := binding.Repository().ListPolicies(t.Context(), "workspace-a"); err != nil || len(policies) != 1 {
		t.Fatalf("rollback leaked policy: policies=%#v err=%v", policies, err)
	}
	var lifecycleRows int
	if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _schema_migrations").Scan(&lifecycleRows); err != nil || lifecycleRows != 1 {
		t.Fatalf("migration ledger rows=%d err=%v", lifecycleRows, err)
	}
}
