package module

import (
	"context"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
	metadatamodule "github.com/domainry/domainry-metadata/module"
)

// newIntegrationDefinitionStore deliberately opens a second Store instance
// over the same physical Metadata tables used by the Lifecycle module. This
// proves the composition boundary shares tables, not Store objects.
func newIntegrationDefinitionStore(t interface {
	Helper()
	Fatal(...any)
}, host integrationHost) metadatasdk.DefinitionStore {
	t.Helper()
	store, err := metadatamodule.OpenDefinitionStore(context.Background(), metadatasdk.ApplicationRef{InstallationID: "integration-test"}, integrationMetadataHost{host: host})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type integrationMetadataHost struct{ host integrationHost }

func (h integrationMetadataHost) Database() metadatamodulehost.Database { return h.host.db }
func (h integrationMetadataHost) Dialect() metadatamodulehost.Dialect   { return h.host.dialect }
func (h integrationMetadataHost) Migrations() metadatamodulehost.MigrationRegistrar {
	return integrationMetadataMigrations{host: h.host}
}

type integrationMetadataMigrations struct{ host integrationHost }

func (integrationMetadataMigrations) Driver() string { return "sqlite" }
func (integrationMetadataMigrations) Schema() string { return "" }
func (m integrationMetadataMigrations) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []metadatamodulehost.SchemaMigration) error {
	return m.host.registrar.ApplyOwnedMigrations(ctx, owner, migrations)
}
