// Package module assembles the embedded Lifecycle implementation over
// host-owned infrastructure.
package module

import (
	"context"
	"fmt"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclesdkadapter "github.com/domainry/domainry-lifecycle/internal/adapter/lifecyclesdk"
	persistence "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
	metadatamodule "github.com/domainry/domainry-metadata/module"
)

type Options struct{}

func OptionsFromEnvironment() Options { return Options{} }

type Factory struct{ options Options }

func NewFactory(options Options) *Factory { return &Factory{options: options} }

func (*Factory) OpenModule(ctx context.Context, application lifecyclesdk.ApplicationRef, host modulehost.Host) (lifecyclesdk.Binding, error) {
	if err := application.Validate(); err != nil {
		return nil, err
	}
	if host == nil || host.Database() == nil || host.Dialect() == nil || host.Migrations() == nil || host.Transactions() == nil {
		return nil, fmt.Errorf("Lifecycle module requires database, dialect, migrations, and transactions")
	}
	auditHost, ok := host.(modulehost.AuditStoreHost)
	if !ok || auditHost.AuditAppender() == nil || auditHost.AuditTransactionalAppender() == nil {
		return nil, fmt.Errorf("Lifecycle shared Audit appenders are required")
	}
	artifactHost, ok := host.(modulehost.ArtifactStoreHost)
	if !ok || artifactHost.ArtifactStore() == nil || artifactHost.ArtifactContentStore() == nil || artifactHost.ArtifactContentWriter() == nil {
		return nil, fmt.Errorf("Lifecycle shared Artifact store and content ports are required")
	}
	if err := persistence.ApplySchema(ctx, host); err != nil {
		return nil, err
	}
	definitions, err := metadatamodule.OpenDefinitionStore(ctx, metadatasdk.ApplicationRef{InstallationID: application.RuntimeID}, lifecycleMetadataHost{host: host})
	if err != nil {
		return nil, fmt.Errorf("open Lifecycle Definition persistence: %w", err)
	}
	return lifecyclesdkadapter.NewBinding(host, definitions)
}

type lifecycleMetadataHost struct{ host modulehost.Host }

func (h lifecycleMetadataHost) Database() metadatamodulehost.Database { return h.host.Database() }
func (h lifecycleMetadataHost) Dialect() metadatamodulehost.Dialect   { return h.host.Dialect() }
func (h lifecycleMetadataHost) Migrations() metadatamodulehost.MigrationRegistrar {
	return lifecycleMetadataMigrations{host: h.host}
}

type lifecycleMetadataMigrations struct{ host modulehost.Host }

func (m lifecycleMetadataMigrations) Driver() string { return string(m.host.Dialect().Name()) }
func (lifecycleMetadataMigrations) Schema() string   { return "" }
func (m lifecycleMetadataMigrations) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []metadatamodulehost.SchemaMigration) error {
	return m.host.Migrations().ApplyOwnedMigrations(ctx, owner, migrations)
}

var _ lifecyclesdk.Factory = (*Factory)(nil)
