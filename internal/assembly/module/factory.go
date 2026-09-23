// Package module assembles the embedded Lifecycle implementation over
// host-owned infrastructure.
package module

import (
	"context"
	"fmt"

	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclesdkadapter "github.com/domainry/domainry-lifecycle/internal/adapter/lifecyclesdk"
	persistence "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
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
	if !ok || artifactHost.ArtifactContentStore() == nil || artifactHost.ArtifactContentWriter() == nil {
		return nil, fmt.Errorf("Lifecycle Artifact content ports are required")
	}
	if err := persistence.ApplySchema(ctx, host); err != nil {
		return nil, err
	}
	definitionKernel, err := shareddefinition.Open(ctx, application.RuntimeID, host.Database(), host.Dialect(), lifecycleDefinitionMigrations{host: host})
	if err != nil {
		return nil, fmt.Errorf("open Lifecycle Definition persistence: %w", err)
	}
	artifacts, err := sharedartifact.Open(ctx, host.Database(), host.Dialect(), host.Migrations())
	if err != nil {
		return nil, fmt.Errorf("open Lifecycle Artifact persistence: %w", err)
	}
	definitions := metadatasdk.AdaptDefinitionStore(definitionKernel)
	return lifecyclesdkadapter.NewBinding(lifecyclePersistenceHost{Host: host, audit: auditHost, artifact: artifactHost, artifacts: artifacts}, definitions)
}

type lifecyclePersistenceHost struct {
	modulehost.Host
	audit     modulehost.AuditStoreHost
	artifact  modulehost.ArtifactStoreHost
	artifacts sharedartifact.ManagedStore
}

func (h lifecyclePersistenceHost) AuditAppender() auditcontract.Appender {
	return h.audit.AuditAppender()
}
func (h lifecyclePersistenceHost) AuditTransactionalAppender() auditcontract.TransactionalAppender {
	return h.audit.AuditTransactionalAppender()
}
func (h lifecyclePersistenceHost) ArtifactStore() sharedartifact.ManagedStore {
	return h.artifacts
}
func (h lifecyclePersistenceHost) ArtifactContentStore() lifecyclecontract.ArtifactContentStore {
	return h.artifact.ArtifactContentStore()
}
func (h lifecyclePersistenceHost) ArtifactContentWriter() lifecyclecontract.ArtifactContentWriter {
	return h.artifact.ArtifactContentWriter()
}

type lifecycleDefinitionMigrations struct{ host modulehost.Host }

func (m lifecycleDefinitionMigrations) Driver() string { return string(m.host.Dialect().Name()) }
func (lifecycleDefinitionMigrations) Schema() string   { return "" }
func (m lifecycleDefinitionMigrations) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []shareddefinition.SchemaMigration) error {
	return m.host.Migrations().ApplyOwnedMigrations(ctx, owner, migrations)
}

var _ lifecyclesdk.Factory = (*Factory)(nil)
