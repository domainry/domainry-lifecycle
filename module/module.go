// Package module is the supported composition entrypoint for embedding
// Lifecycle into Runtime or another Domainry host.
package module

import (
	"context"
	"fmt"
	"strings"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	"github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	"github.com/domainry/domainry-lifecycle-sdk/repository"
	artifactstore "github.com/domainry/domainry-lifecycle/internal/infrastructure/artifact/filesystem"
	persistence "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/lifecycle"
	schema "github.com/domainry/domainry-lifecycle/internal/infrastructure/persistence/database/schema"
)

type Factory struct{}

func NewFactory() *Factory { return &Factory{} }

const MigrationOwner = schema.Owner

// SchemaMigrations exposes Lifecycle-owned DDL to hosts that assemble the
// shared database before opening the full Module Binding. The host remains
// the sole owner of the migration lock and _schema_migrations ledger.
func SchemaMigrations(dialect modulehost.Dialect) ([]modulehost.SchemaMigration, error) {
	return schema.Migrations(dialect)
}

func (*Factory) OpenModule(ctx context.Context, application lifecyclesdk.ApplicationRef, host modulehost.Host) (lifecyclesdk.Binding, error) {
	if err := application.Validate(); err != nil {
		return nil, err
	}
	if host == nil || host.Database() == nil || host.Dialect() == nil || host.Migrations() == nil || host.Transactions() == nil {
		return nil, fmt.Errorf("Lifecycle module requires database, dialect, migrations, and transactions")
	}
	if err := schema.Apply(ctx, host); err != nil {
		return nil, err
	}
	return &binding{repository: persistence.NewLifecycleStore(host), host: host, transactions: host.Transactions()}, nil
}

type binding struct {
	repository   persistence.LifecycleStore
	host         modulehost.Host
	transactions modulehost.Transactor
}

func (*binding) Descriptor() lifecyclesdk.Descriptor {
	return lifecyclesdk.Descriptor{
		ProtocolVersion: lifecyclesdk.ProtocolVersionV1,
		Mode:            lifecyclesdk.DeploymentModeModule,
		Capabilities: lifecyclesdk.Capabilities{
			Governance: true, SubjectRequests: true, RetentionWorker: true,
			UploadArtifacts: true, ArchiveEvidence: true,
		},
	}
}

func (b *binding) Repository() repository.LifecycleRepository {
	if b == nil {
		return nil
	}
	return b.repository
}

func (b *binding) UploadArtifacts(options lifecyclesdk.UploadArtifactOptions) (contract.UploadFileArtifactStore, error) {
	if b == nil || b.host == nil || strings.TrimSpace(options.Root) == "" || options.Fields == nil {
		return nil, fmt.Errorf("Lifecycle upload artifact options are incomplete")
	}
	storeOptions := make([]persistence.FileArtifactStoreOption, 0, 2)
	if options.References != nil {
		storeOptions = append(storeOptions, persistence.WithUploadArtifactReferences(options.References))
	}
	if options.ExpiredReferences != nil {
		storeOptions = append(storeOptions, persistence.WithExpiredUploadReferenceCleaner(options.ExpiredReferences))
	}
	return persistence.NewFileArtifactStore(b.host, options.Fields, options.Root, storeOptions...), nil
}

func (*binding) SubjectArtifacts(root string) (contract.SubjectArtifactStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("Lifecycle subject artifact root is required")
	}
	return artifactstore.NewSubjectStore(root), nil
}

func (b *binding) ArchiveStore() contract.ArchiveStore {
	if b == nil {
		return persistence.NewArchiveWriter(nil)
	}
	return persistence.NewArchiveWriter(b.host)
}

// WithinTransaction executes a Lifecycle use case against the host transaction
// boundary without exposing the host DBTX through the SDK.
func (b *binding) WithinTransaction(ctx context.Context, operation func(context.Context) error) error {
	if b == nil || b.transactions == nil || operation == nil {
		return fmt.Errorf("Lifecycle transaction requires binding and operation")
	}
	return b.transactions.WithinTransaction(ctx, func(transactionContext context.Context, _ modulehost.DBTX) error {
		return operation(transactionContext)
	})
}

func (*binding) Close(context.Context) error { return nil }

var _ lifecyclesdk.Factory = (*Factory)(nil)
var _ lifecyclesdk.Binding = (*binding)(nil)
