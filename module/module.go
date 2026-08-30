// Package module is the supported composition entrypoint for embedding
// Lifecycle into Runtime, Identity, Party, or another Domainry SaaS.
package module

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-lifecycle/application"
	"github.com/domainry/domainry-lifecycle/contract"
	"github.com/domainry/domainry-lifecycle/migrations"
	"github.com/domainry/domainry-lifecycle/modulehost"
	"github.com/domainry/domainry-lifecycle/persistence"
)

type Options struct {
	Application             application.LifecycleApplicationDependencies
	UploadRoot              string
	UploadFields            contract.UploadFieldCatalog
	UploadReferences        contract.UploadArtifactReferenceResolver
	ExpiredUploadReferences contract.ExpiredUploadReferenceCleaner
}

type Binding struct {
	Repository      persistence.LifecycleStore
	Service         *application.LifecycleApplicationService
	UploadArtifacts *persistence.FileArtifactStore
	transactions    modulehost.Transactor
}

func Bind(ctx context.Context, host modulehost.Host, options Options) (*Binding, error) {
	if host == nil || host.Database() == nil || host.Dialect() == nil || host.Migrations() == nil || host.Transactions() == nil {
		return nil, fmt.Errorf("lifecycle module requires database, dialect, migrations, and transactions")
	}
	if err := migrations.Apply(ctx, host); err != nil {
		return nil, err
	}
	repository := persistence.NewLifecycleStore(host)
	dependencies := options.Application
	if dependencies.Repository == nil && dependencies.Policies == nil && dependencies.LegalHolds == nil && dependencies.CleanupJobs == nil && dependencies.SubjectRequests == nil && dependencies.Evidence == nil {
		dependencies.Repository = repository
	}
	binding := &Binding{Repository: repository, Service: application.NewLifecycleApplicationService(ctx, dependencies), transactions: host.Transactions()}
	if strings.TrimSpace(options.UploadRoot) != "" {
		fileOptions := make([]persistence.FileArtifactStoreOption, 0, 2)
		if options.UploadReferences != nil {
			fileOptions = append(fileOptions, persistence.WithUploadArtifactReferences(options.UploadReferences))
		}
		if options.ExpiredUploadReferences != nil {
			fileOptions = append(fileOptions, persistence.WithExpiredUploadReferenceCleaner(options.ExpiredUploadReferences))
		}
		binding.UploadArtifacts = persistence.NewFileArtifactStore(host, options.UploadFields, options.UploadRoot, fileOptions...)
	}
	return binding, nil
}

// WithinTransaction executes a Lifecycle use case against the host's
// transaction boundary. Every bundled persistence adapter resolves the bound
// DBTX from ctx, so repository calls in operation are atomic without opening
// a module-owned transaction.
func (b *Binding) WithinTransaction(ctx context.Context, operation func(context.Context, *Binding) error) error {
	if b == nil || b.transactions == nil || operation == nil {
		return fmt.Errorf("lifecycle transaction requires binding and operation")
	}
	return b.transactions.WithinTransaction(ctx, func(transactionContext context.Context, _ modulehost.DBTX) error {
		return operation(transactionContext, b)
	})
}
