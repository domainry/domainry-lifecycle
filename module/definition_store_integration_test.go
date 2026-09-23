package module

import (
	"context"

	shareddefinition "github.com/domainry/domainry-foundation/definition"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
)

// newIntegrationDefinitionStore deliberately opens a second Store instance
// over the same physical Metadata tables used by the Lifecycle module. This
// proves the composition boundary shares tables, not Store objects.
func newIntegrationDefinitionStore(t interface {
	Helper()
	Fatal(...any)
}, host integrationHost) metadatasdk.DefinitionStore {
	t.Helper()
	store, err := shareddefinition.Open(context.Background(), "integration-test", host.db, host.dialect, integrationDefinitionMigrations{host: host})
	if err != nil {
		t.Fatal(err)
	}
	return metadatasdk.AdaptDefinitionStore(store)
}

type integrationDefinitionMigrations struct{ host integrationHost }

func (integrationDefinitionMigrations) Driver() string { return "sqlite" }
func (integrationDefinitionMigrations) Schema() string { return "" }
func (m integrationDefinitionMigrations) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []shareddefinition.SchemaMigration) error {
	return m.host.registrar.ApplyOwnedMigrations(ctx, owner, migrations)
}
