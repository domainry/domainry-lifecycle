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
	definitionHost, ok := host.(modulehost.DefinitionStoreHost)
	if !ok || definitionHost.DefinitionStore() == nil {
		return nil, fmt.Errorf("Lifecycle shared Definition store is required")
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
	return lifecyclesdkadapter.NewBinding(host)
}

var _ lifecyclesdk.Factory = (*Factory)(nil)
